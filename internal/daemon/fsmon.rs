//! OS-native file monitoring: kernel event streams, zero polling.
//!
//! * Linux:   `inotify` (recursive watches, overflow detection).
//! * macOS:   `FSEvents` (framework loaded dynamically, no link dependency).
//! * Windows: blocking `ReadDirectoryChangesW` threads (kernel-woken).
//!
//! Event kinds match the daemon wire protocol:
//! 1 = create, 2 = modify, 3 = delete, 4 = rename, 5 = attrib, 6 = overflow.

use std::collections::HashMap;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::time::Duration;

#[derive(Debug, Clone)]
pub struct FileEvent {
    pub kind: u32,
    pub cookie: u32,
    pub path: PathBuf,
}

const IGNORED: &[&str] = &[".git", ".hg", ".svn", "node_modules", "target", ".ebuild"];

fn ignored(name: &std::ffi::OsStr) -> bool {
    name.to_str().map(|s| IGNORED.contains(&s)).unwrap_or(false)
}

enum Command {
    Add(PathBuf, bool),
}

pub struct Watcher {
    tx: mpsc::Sender<Command>,
    rx: mpsc::Receiver<FileEvent>,
}

impl Watcher {
    pub fn new() -> io::Result<Self> {
        let (etx, erx) = mpsc::channel::<FileEvent>();
        let (ctx, crx) = mpsc::channel::<Command>();
        std::thread::Builder::new()
            .name("ebuild-fsmon".into())
            .spawn(move || backend_main(etx, crx))?;
        Ok(Watcher { tx: ctx, rx: erx })
    }

    pub fn watch(&self, path: &Path, recursive: bool) -> io::Result<()> {
        self.tx
            .send(Command::Add(path.to_path_buf(), recursive))
            .map_err(|_| io::Error::new(io::ErrorKind::BrokenPipe, "fsmon thread exited"))
    }

    pub fn recv_timeout(&self, timeout: Duration) -> Option<FileEvent> {
        self.rx.recv_timeout(timeout).ok()
    }
}

// ================================================================ Linux ====
#[cfg(target_os = "linux")]
fn backend_main(etx: mpsc::Sender<FileEvent>, crx: mpsc::Receiver<Command>) {
    use libc::{
        c_void, close, inotify_add_watch, inotify_init1, poll, pollfd, read, IN_ATTRIB, IN_CLOEXEC,
        IN_CREATE, IN_DELETE, IN_DELETE_SELF, IN_IGNORED, IN_ISDIR, IN_MODIFY, IN_MOVED_FROM,
        IN_MOVED_TO, IN_MOVE_SELF, IN_NONBLOCK, IN_Q_OVERFLOW, POLLIN,
    };
    const MASK: u32 = IN_CREATE
        | IN_MODIFY
        | IN_DELETE
        | IN_MOVED_FROM
        | IN_MOVED_TO
        | IN_ATTRIB
        | IN_DELETE_SELF
        | IN_MOVE_SELF;
    // inotify_event fixed prefix: wd(i32) mask(u32) cookie(u32) len(u32).
    const HDR: usize = 16;

    unsafe fn add_one(fd: i32, wds: &mut HashMap<i32, PathBuf>, dir: &Path) {
        use std::ffi::CString;
        let Ok(c) = CString::new(dir.as_os_str().as_encoded_bytes()) else {
            return;
        };
        let wd = inotify_add_watch(fd, c.as_ptr(), MASK);
        if wd < 0 {
            return;
        }
        wds.insert(wd, dir.to_path_buf());
    }

    unsafe fn scan(fd: i32, wds: &mut HashMap<i32, PathBuf>, dir: &Path, recursive: bool) {
        add_one(fd, wds, dir);
        if !recursive {
            return;
        }
        let Ok(rd) = std::fs::read_dir(dir) else {
            return;
        };
        for e in rd.flatten() {
            let p = e.path();
            if p.is_dir() && !ignored(e.file_name().as_os_str()) {
                scan(fd, wds, &p, true);
            }
        }
    }

    let fd = unsafe { inotify_init1(IN_NONBLOCK | IN_CLOEXEC) };
    if fd < 0 {
        return;
    }
    let mut wds: HashMap<i32, PathBuf> = HashMap::new();
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        // Drain pending watch commands first (non-blocking).
        while let Ok(Command::Add(p, rec)) = crx.try_recv() {
            unsafe { scan(fd, &mut wds, &p, rec) };
        }
        // Block in poll() until the kernel reports events (no FS polling).
        let mut pfd = pollfd {
            fd,
            events: POLLIN,
            revents: 0,
        };
        let r = unsafe { poll(&mut pfd, 1, 200) };
        if r < 0 {
            break;
        }
        if r == 0 {
            continue;
        }
        let n = unsafe { read(fd, buf.as_mut_ptr() as *mut c_void, buf.len()) };
        if n <= 0 {
            continue;
        }
        let mut off = 0usize;
        let n = n as usize;
        while off + HDR <= n {
            let wd = i32::from_ne_bytes(buf[off..off + 4].try_into().unwrap());
            let mask = u32::from_ne_bytes(buf[off + 4..off + 8].try_into().unwrap());
            let cookie = u32::from_ne_bytes(buf[off + 8..off + 12].try_into().unwrap());
            let len = u32::from_ne_bytes(buf[off + 12..off + 16].try_into().unwrap()) as usize;
            if off + HDR + len > n {
                break;
            }
            if mask & IN_Q_OVERFLOW != 0 {
                let _ = etx.send(FileEvent {
                    kind: 6,
                    cookie: 0,
                    path: PathBuf::new(),
                });
                off += HDR + len;
                continue;
            }
            let Some(dir) = wds.get(&wd).cloned() else {
                off += HDR + len;
                continue;
            };
            if mask & IN_IGNORED != 0 {
                wds.remove(&wd);
                off += HDR + len;
                continue;
            }
            let name = if len > 0 {
                let raw = &buf[off + HDR..off + HDR + len];
                let end = raw.iter().position(|&b| b == 0).unwrap_or(raw.len());
                // SAFETY: inotify hands back the exact bytes the kernel
                // stores; Unix paths are opaque byte strings.
                unsafe { std::ffi::OsStr::from_encoded_bytes_unchecked(&raw[..end]) }
            } else {
                std::ffi::OsStr::new("")
            };
            let full = if name.is_empty() { dir } else { dir.join(name) };
            let kind = if mask & (IN_CREATE | IN_MOVED_TO) != 0 {
                1
            } else if mask & IN_MODIFY != 0 {
                2
            } else if mask & (IN_DELETE | IN_DELETE_SELF | IN_MOVED_FROM) != 0 {
                3
            } else if mask & IN_MOVE_SELF != 0 {
                4
            } else if mask & IN_ATTRIB != 0 {
                5
            } else {
                off += HDR + len;
                continue;
            };
            if kind == 1 && mask & IN_ISDIR != 0 {
                unsafe { add_one(fd, &mut wds, &full) };
            }
            if etx
                .send(FileEvent {
                    kind,
                    cookie,
                    path: full,
                })
                .is_err()
            {
                unsafe { close(fd) };
                return;
            }
            off += HDR + len;
        }
    }
    unsafe { close(fd) };
}

// ================================================================ macOS ====
#[cfg(target_os = "macos")]
fn backend_main(etx: mpsc::Sender<FileEvent>, crx: mpsc::Receiver<Command>) {
    use std::ffi::{c_void, CStr, CString};
    use std::os::raw::{c_char, c_double, c_uchar};

    type CFAllocatorRef = *const c_void;
    type CFRunLoopRef = *mut c_void;
    type CFStringRef = *const c_void;
    type CFArrayRef = *const c_void;
    type FSEventStreamRef = *mut c_void;
    type FSEventStreamEventId = u64;
    type FSEventStreamEventFlags = u32;
    type CFIndex = isize;

    type Cb = extern "C" fn(
        *const c_void,
        *mut c_void,
        usize,
        *mut c_void,
        *const FSEventStreamEventFlags,
        *const FSEventStreamEventId,
    );

    #[repr(C)]
    struct FSEventStreamContext {
        version: isize,
        info: *mut c_void,
        retain: *const c_void,
        release: *const c_void,
        copy_description: *const c_void,
    }

    struct Lib {
        create: unsafe extern "C" fn(
            CFAllocatorRef,
            Cb,
            *mut FSEventStreamContext,
            CFArrayRef,
            FSEventStreamEventId,
            c_double,
            u32,
        ) -> FSEventStreamRef,
        schedule: unsafe extern "C" fn(FSEventStreamRef, CFRunLoopRef, CFStringRef),
        start: unsafe extern "C" fn(FSEventStreamRef) -> c_uchar,
        runloop_current: unsafe extern "C" fn() -> CFRunLoopRef,
        runloop_run: unsafe extern "C" fn(),
        str_create: unsafe extern "C" fn(CFAllocatorRef, *const c_char, u32) -> CFStringRef,
        array_create: unsafe extern "C" fn(
            CFAllocatorRef,
            *const CFStringRef,
            CFIndex,
            *const c_void,
        ) -> CFArrayRef,
        default_mode: CFStringRef,
    }

    const SINCE_NOW: u64 = u64::MAX;
    const FLAG_FILE_EVENTS: u32 = 0x0000_0010;
    const FLAG_NO_DEFER: u32 = 0x0000_0020;
    const F_CREATED: u32 = 0x0000_0100;
    const F_REMOVED: u32 = 0x0000_0200;
    const F_INODE_META: u32 = 0x0000_0400;
    const F_RENAMED: u32 = 0x0000_0800;
    const F_MUST_SCAN: u32 = 0x0000_0001;

    extern "C" fn callback(
        info: *const c_void,
        _stream: *mut c_void,
        n: usize,
        paths: *mut c_void,
        flags: *const FSEventStreamEventFlags,
        _ids: *const FSEventStreamEventId,
    ) {
        let tx = unsafe { &*(info as *const mpsc::Sender<FileEvent>) };
        let arr = paths as *const *const c_char;
        for i in 0..n {
            let cstr = unsafe { CStr::from_ptr(*arr.add(i)) };
            let path = PathBuf::from(cstr.to_string_lossy().into_owned());
            let f = if flags.is_null() {
                0
            } else {
                unsafe { *flags.add(i) }
            };
            let kind = if f & F_MUST_SCAN != 0 {
                6
            } else if f & F_CREATED != 0 {
                1
            } else if f & F_REMOVED != 0 {
                3
            } else if f & F_RENAMED != 0 {
                4
            } else if f & F_INODE_META != 0 {
                5
            } else {
                2
            };
            if tx
                .send(FileEvent {
                    kind,
                    cookie: 0,
                    path,
                })
                .is_err()
            {
                return;
            }
        }
    }

    unsafe fn load() -> Option<Lib> {
        let rt = libc::dlopen(
            CString::new("/System/Library/Frameworks/CoreServices.framework/CoreServices")
                .ok()?
                .as_ptr(),
            libc::RTLD_NOW | libc::RTLD_LOCAL,
        );
        if rt.is_null() {
            return None;
        }
        macro_rules! sym {
            ($name:expr, $ty:ty) => {{
                let s = CString::new($name).unwrap();
                let p = libc::dlsym(rt, s.as_ptr());
                if p.is_null() {
                    return None;
                }
                std::mem::transmute::<*mut c_void, $ty>(p)
            }};
        }
        let mode_name = CString::new("kCFRunLoopDefaultMode").unwrap();
        let mode_ptr_ptr = libc::dlsym(rt, mode_name.as_ptr()) as *const CFStringRef;
        if mode_ptr_ptr.is_null() {
            return None;
        }
        Some(Lib {
            create: sym!(
                "FSEventStreamCreate",
                unsafe extern "C" fn(
                    CFAllocatorRef,
                    Cb,
                    *mut FSEventStreamContext,
                    CFArrayRef,
                    FSEventStreamEventId,
                    c_double,
                    u32,
                ) -> FSEventStreamRef
            ),
            schedule: sym!(
                "FSEventStreamScheduleWithRunLoop",
                unsafe extern "C" fn(FSEventStreamRef, CFRunLoopRef, CFStringRef)
            ),
            start: sym!(
                "FSEventStreamStart",
                unsafe extern "C" fn(FSEventStreamRef) -> c_uchar
            ),
            runloop_current: sym!(
                "CFRunLoopGetCurrent",
                unsafe extern "C" fn() -> CFRunLoopRef
            ),
            runloop_run: sym!("CFRunLoopRun", unsafe extern "C" fn()),
            str_create: sym!(
                "CFStringCreateWithCString",
                unsafe extern "C" fn(CFAllocatorRef, *const c_char, u32) -> CFStringRef
            ),
            array_create: sym!(
                "CFArrayCreate",
                unsafe extern "C" fn(
                    CFAllocatorRef,
                    *const CFStringRef,
                    CFIndex,
                    *const c_void,
                ) -> CFArrayRef
            ),
            default_mode: *mode_ptr_ptr,
        })
    }

    let Some(lib) = (unsafe { load() }) else {
        return;
    };
    let lib = std::sync::Arc::new(lib);
    // One stream + run loop per watched root. Multiple CFRunLoops in a single
    // process are fully supported, and every stream is subtree-recursive, so
    // each root is independent. Senders are daemon-lifetime: leaked per thread.
    for Command::Add(root, _rec) in crx {
        let etx = etx.clone();
        let lib = lib.clone();
        std::thread::Builder::new()
            .name("ebuild-fsevents".into())
            .spawn(move || {
                let info: *mut c_void = Box::into_raw(Box::new(etx)) as *mut c_void;
                let cpath = match CString::new(root.as_os_str().as_encoded_bytes()) {
                    Ok(c) => c,
                    Err(_) => return,
                };
                unsafe {
                    let s = (lib.str_create)(std::ptr::null(), cpath.as_ptr(), 0x0800_0100);
                    if s.is_null() {
                        return;
                    }
                    let arr = (lib.array_create)(std::ptr::null(), &s, 1, std::ptr::null());
                    let mut ctx = FSEventStreamContext {
                        version: 0,
                        info,
                        retain: std::ptr::null(),
                        release: std::ptr::null(),
                        copy_description: std::ptr::null(),
                    };
                    let stream = (lib.create)(
                        std::ptr::null(),
                        callback,
                        &mut ctx,
                        arr,
                        SINCE_NOW,
                        0.05,
                        FLAG_FILE_EVENTS | FLAG_NO_DEFER,
                    );
                    if stream.is_null() {
                        return;
                    }
                    let rl = (lib.runloop_current)();
                    (lib.schedule)(stream, rl, lib.default_mode);
                    if (lib.start)(stream) == 0 {
                        return;
                    }
                    (lib.runloop_run)();
                }
            })
            .ok();
    }
}

// ============================================================== Windows ====
#[cfg(target_os = "windows")]
fn backend_main(etx: mpsc::Sender<FileEvent>, crx: mpsc::Receiver<Command>) {
    use std::ffi::c_void;
    use std::os::windows::ffi::OsStrExt;

    type HANDLE = isize;
    type DWORD = u32;
    type BOOL = i32;
    type LPCWSTR = *const u16;
    type LPVOID = *mut c_void;

    const INVALID_HANDLE_VALUE: HANDLE = -1;
    const OPEN_EXISTING: DWORD = 3;
    const FILE_SHARE_ALL: DWORD = 0x7;
    const FILE_FLAG_BACKUP_SEMANTICS: DWORD = 0x0200_0000;
    const FILE_LIST_DIRECTORY: DWORD = 0x0001;
    const NOTIFY_ALL: DWORD = 0x17F; // file+dir names, attrs, size, times

    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn CreateFileW(
            name: LPCWSTR,
            access: DWORD,
            share: DWORD,
            sec: LPVOID,
            disp: DWORD,
            flags: DWORD,
            tmpl: HANDLE,
        ) -> HANDLE;
        fn ReadDirectoryChangesW(
            dir: HANDLE,
            buf: LPVOID,
            len: DWORD,
            subtree: BOOL,
            filter: DWORD,
            out: *mut DWORD,
            ov: LPVOID,
            cb: LPVOID,
        ) -> BOOL;
        fn CloseHandle(h: HANDLE) -> BOOL;
    }

    #[repr(C)]
    struct Notify {
        next: DWORD,
        action: DWORD,
        name_len: DWORD, // bytes
                         // name: [u16] follows
    }

    fn wide(s: &Path) -> Vec<u16> {
        s.as_os_str().encode_wide().chain([0]).collect()
    }

    fn spawn_dir(etx: mpsc::Sender<FileEvent>, dir: PathBuf) {
        std::thread::Builder::new()
            .name("ebuild-rdcw".into())
            .spawn(move || {
                let w = wide(&dir);
                let h = unsafe {
                    CreateFileW(
                        w.as_ptr(),
                        FILE_LIST_DIRECTORY,
                        FILE_SHARE_ALL,
                        std::ptr::null_mut(),
                        OPEN_EXISTING,
                        FILE_FLAG_BACKUP_SEMANTICS,
                        0,
                    )
                };
                if h == INVALID_HANDLE_VALUE {
                    return;
                }
                let mut buf = vec![0u8; 64 * 1024];
                loop {
                    let mut out: DWORD = 0;
                    // Blocking kernel wait: woken only when something changed.
                    let ok = unsafe {
                        ReadDirectoryChangesW(
                            h,
                            buf.as_mut_ptr() as LPVOID,
                            buf.len() as DWORD,
                            1,
                            NOTIFY_ALL,
                            &mut out,
                            std::ptr::null_mut(),
                            std::ptr::null_mut(),
                        )
                    };
                    if ok == 0 {
                        break;
                    }
                    let mut off = 0usize;
                    let total = out as usize;
                    while off + 12 <= total {
                        let n = unsafe { &*(buf.as_ptr().add(off) as *const Notify) };
                        let wlen = (n.name_len / 2) as usize;
                        let base = off + 12;
                        if base + wlen * 2 > total {
                            break;
                        }
                        let ws = unsafe {
                            std::slice::from_raw_parts(buf.as_ptr().add(base) as *const u16, wlen)
                        };
                        let rel = String::from_utf16_lossy(ws);
                        let full = dir.join(rel.replace('\\', "/"));
                        let kind = match n.action {
                            1 | 5 => 1, // ADDED / RENAMED_NEW
                            2 | 4 => {
                                // REMOVED / RENAMED_OLD → delete+rename both
                                // useful; protocol carries one kind: rename
                                // only when paired handling exists. Emit 3
                                // for REMOVED, 4 for RENAMED_OLD.
                                if n.action == 4 {
                                    4
                                } else {
                                    3
                                }
                            }
                            3 => 2, // MODIFIED
                            _ => 5,
                        };
                        if etx
                            .send(FileEvent {
                                kind,
                                cookie: 0,
                                path: full,
                            })
                            .is_err()
                        {
                            unsafe { CloseHandle(h) };
                            return;
                        }
                        if n.next == 0 {
                            break;
                        }
                        off += n.next as usize;
                    }
                }
                unsafe { CloseHandle(h) };
            })
            .ok();
    }

    for Command::Add(root, _rec) in crx {
        // ReadDirectoryChangesW is inherently subtree-recursive.
        spawn_dir(etx.clone(), root);
    }
}

// ================================================================ other ====
#[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
fn backend_main(_etx: mpsc::Sender<FileEvent>, crx: mpsc::Receiver<Command>) {
    // Unsupported OS: drain commands so watch() never blocks.
    for _ in crx {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[cfg(target_os = "linux")]
    fn inotify_create_fires() {
        let dir = std::env::temp_dir().join(format!("ebuild-fsmon-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let w = Watcher::new().unwrap();
        w.watch(&dir, true).unwrap();
        std::thread::sleep(Duration::from_millis(300)); // let the backend arm the watch
        std::fs::write(dir.join("hello.c"), "int x;\n").unwrap();
        let ev = w.recv_timeout(Duration::from_secs(5)).expect("no event");
        assert_eq!(ev.kind, 1, "expected create, got {:?}", ev);
        assert!(ev.path.ends_with("hello.c"));
        let _ = std::fs::remove_dir_all(&dir);
    }
}
