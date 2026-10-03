//! Zero-copy IPC: lock-free MPMC ring buffer in `mmap`'d shared memory.
//!
//! Layout (all little-endian, all atomics 8-byte aligned):
//! ```text
//! offset  size  field
//! 0       8     magic  (b"EBUILDRG" = 0x4752444C49554245)
//! 8       8     slots  (power of two)
//! 16      8     slot_size (1024)
//! 24      8     head   (AtomicU64, producer cursor)
//! 32      8     tail   (AtomicU64, consumer cursor)
//! 40      24    reserved
//! 64      …     slots: [kind u32][cookie u32][path_len u32][pad u32]
//!               [path bytes …][seq AtomicU64 @ +1016]
//! ```
//! Producers CAS `head`; consumers CAS `tail` (Vyukov sequence cells).
//! Any process mapping the file — including the Go orchestrator via
//! `internal/daemon/shm_reader.go` — reads events with no syscalls,
//! no serialization, and no heap allocation on the hot path.

use std::io;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

pub const MAGIC: u64 = 0x4752_444c_4955_4245;
pub const HEADER_LEN: usize = 64;
pub const SLOT_SIZE: usize = 1024;
pub const MAX_PATH: usize = 1008;
pub const SEQ_OFF: usize = 1016;
pub const PATH_OFF: usize = 12;

#[derive(Debug)]
pub enum PushError {
    Full,
    PathTooLong,
}

struct Mapping {
    base: *mut u8,
    len: usize,
    slots: u64,
}

// SAFETY: the mapping is MAP_SHARED memory operated on only via atomics and
// plain byte copies into exclusively-claimed slots. `Mapping` is therefore
// Send + Sync by construction of the ring protocol below.
unsafe impl Send for Mapping {}
unsafe impl Sync for Mapping {}

impl Mapping {
    fn head(&self) -> &AtomicU64 {
        assert_eq!(self.base as usize % 8, 0);
        unsafe { &*(self.base.add(24) as *const AtomicU64) }
    }
    fn tail(&self) -> &AtomicU64 {
        unsafe { &*(self.base.add(32) as *const AtomicU64) }
    }
    fn seq(&self, cell: u64) -> &AtomicU64 {
        unsafe {
            &*(self
                .base
                .add(HEADER_LEN + cell as usize * SLOT_SIZE + SEQ_OFF)
                as *const AtomicU64)
        }
    }
    fn slot(&self, cell: u64) -> *mut u8 {
        unsafe { self.base.add(HEADER_LEN + cell as usize * SLOT_SIZE) }
    }
    fn mask(&self) -> u64 {
        self.slots - 1
    }
}

impl Drop for Mapping {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.base as *mut libc::c_void, self.len);
        }
    }
}

fn map_file(path: &Path, len: usize, create: bool) -> io::Result<(i32, *mut u8)> {
    use std::ffi::CString;
    use std::os::unix::ffi::OsStrExt;
    let c = CString::new(path.as_os_str().as_bytes())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "shm path contains NUL"))?;
    let flags = libc::O_RDWR
        | libc::O_CLOEXEC
        | if create {
            libc::O_CREAT | libc::O_EXCL
        } else {
            0
        };
    // SAFETY: direct syscall wrappers; CString is NUL-terminated.
    let fd = unsafe { libc::open(c.as_ptr(), flags, 0o600) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    if create {
        let r = unsafe { libc::ftruncate(fd, len as libc::off_t) };
        if r != 0 {
            let e = io::Error::last_os_error();
            unsafe { libc::close(fd) };
            return Err(e);
        }
    }
    let base = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            len,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_SHARED,
            fd,
            0,
        )
    };
    if base == libc::MAP_FAILED {
        let e = io::Error::last_os_error();
        unsafe { libc::close(fd) };
        return Err(e);
    }
    Ok((fd, base as *mut u8))
}

fn round_slots(n: u32) -> u64 {
    let mut s = if n < 2 { 2 } else { n as u64 };
    s = s.next_power_of_two();
    s
}

fn spin(n: &mut u32) {
    *n += 1;
    if *n < 1000 {
        std::hint::spin_loop();
    } else {
        std::thread::yield_now();
        *n = 0;
    }
}

pub struct Producer {
    map: Mapping,
    _path: PathBuf,
    fd: i32,
}

impl Producer {
    /// Ring capacity in slots (for status reporting).
    pub fn slots(&self) -> u64 {
        self.map.slots
    }

    /// Create (or attach to) the ring file with at least `slots` slots.
    pub fn open(path: &Path, slots: u32) -> io::Result<Self> {
        let slots = round_slots(slots);
        let len = HEADER_LEN + slots as usize * SLOT_SIZE;
        // Try exclusive create; on EEXIST attach to the existing ring.
        match map_file(path, len, true) {
            Ok((fd, base)) => {
                // Fresh mapping is zeroed by the kernel: write header, then
                // per-slot sequences, then publish the magic last so racing
                // attachers never observe a half-initialized ring.
                unsafe {
                    (base.add(8) as *mut u64).write_unaligned(slots);
                    (base.add(16) as *mut u64).write_unaligned(SLOT_SIZE as u64);
                    for i in 0..slots {
                        (base.add(HEADER_LEN + i as usize * SLOT_SIZE + SEQ_OFF) as *mut u64)
                            .write_unaligned(i);
                    }
                    std::sync::atomic::fence(Ordering::Release);
                    (base as *mut u64).write_unaligned(MAGIC);
                }
                Ok(Producer {
                    map: Mapping { base, len, slots },
                    _path: path.to_path_buf(),
                    fd,
                })
            }
            Err(e)
                if e.kind() == io::ErrorKind::AlreadyExists
                    || e.raw_os_error() == Some(libc::EEXIST) =>
            {
                Self::attach(path, fd_from_open(path)?)
            }
            Err(_) => {
                // open() without O_EXCL also fails EEXIST-shaped errors on
                // some kernels; fall back to attach.
                Self::attach(path, fd_from_open(path)?)
            }
        }
    }

    fn attach(path: &Path, fd: i32) -> io::Result<Self> {
        // Wait briefly for a racing creator to publish the magic.
        for _ in 0..500 {
            let mut magic = 0u64;
            let r = unsafe { libc::pread(fd, &mut magic as *mut u64 as *mut libc::c_void, 8, 0) };
            if r == 8 && magic == MAGIC {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
        let mut hdr = [0u64; 3];
        let r = unsafe { libc::pread(fd, hdr.as_mut_ptr() as *mut libc::c_void, 24, 0) };
        if r != 24 || hdr[0] != MAGIC || hdr[2] != SLOT_SIZE as u64 {
            unsafe { libc::close(fd) };
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "bad shm ring header",
            ));
        }
        let slots = hdr[1];
        let len = HEADER_LEN + slots as usize * SLOT_SIZE;
        let base = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                len,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };
        if base == libc::MAP_FAILED {
            let e = io::Error::last_os_error();
            unsafe { libc::close(fd) };
            return Err(e);
        }
        Ok(Producer {
            map: Mapping {
                base: base as *mut u8,
                len,
                slots,
            },
            _path: path.to_path_buf(),
            fd,
        })
    }

    pub fn try_push(&self, kind: u32, cookie: u32, path: &str) -> Result<(), PushError> {
        let bytes = path.as_bytes();
        if bytes.len() > MAX_PATH {
            return Err(PushError::PathTooLong);
        }
        let m = &self.map;
        let mut pos = m.head().load(Ordering::Relaxed);
        loop {
            let tail = m.tail().load(Ordering::Acquire);
            if pos >= tail + m.slots {
                // Possibly stale head; re-check once before declaring full.
                pos = m.head().load(Ordering::Relaxed);
                if pos >= m.tail().load(Ordering::Acquire) + m.slots {
                    return Err(PushError::Full);
                }
                continue;
            }
            match m
                .head()
                .compare_exchange_weak(pos, pos + 1, Ordering::AcqRel, Ordering::Relaxed)
            {
                Ok(_) => break,
                Err(actual) => pos = actual,
            }
        }
        let cell = pos & m.mask();
        let mut spins = 0u32;
        while m.seq(cell).load(Ordering::Acquire) != pos {
            spin(&mut spins);
        }
        unsafe {
            let slot = m.slot(cell);
            (slot as *mut u32).write_unaligned(kind);
            (slot.add(4) as *mut u32).write_unaligned(cookie);
            (slot.add(8) as *mut u32).write_unaligned(bytes.len() as u32);
            (slot.add(12) as *mut u32).write_unaligned(0);
            std::ptr::copy_nonoverlapping(bytes.as_ptr(), slot.add(PATH_OFF), bytes.len());
        }
        m.seq(cell).store(pos + 1, Ordering::Release);
        Ok(())
    }
}

impl Drop for Producer {
    fn drop(&mut self) {
        unsafe { libc::close(self.fd) };
    }
}

fn fd_from_open(path: &Path) -> io::Result<i32> {
    use std::ffi::CString;
    use std::os::unix::ffi::OsStrExt;
    let c = CString::new(path.as_os_str().as_bytes())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "shm path contains NUL"))?;
    let fd = unsafe { libc::open(c.as_ptr(), libc::O_RDWR | libc::O_CLOEXEC, 0o600) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(fd)
}

// In-process consumer (exercised by unit tests). The production consumer is
// the Go orchestrator (`internal/daemon/shm_reader.go`); this impl is the
// cross-check that the shared-memory protocol is correct.
#[cfg_attr(not(test), allow(dead_code))]
pub struct Consumer {
    map: Mapping,
    fd: i32,
}

#[cfg_attr(not(test), allow(dead_code))]
impl Consumer {
    pub fn open(path: &Path) -> io::Result<Self> {
        let fd = fd_from_open(path)?;
        let prod = Producer::attach(path, fd)?;
        let fd2 = prod.fd;
        let map = Mapping {
            base: prod.map.base,
            len: prod.map.len,
            slots: prod.map.slots,
        };
        std::mem::forget(prod); // keep mapping alive; we own fd via fd2
        Ok(Consumer { map, fd: fd2 })
    }

    pub fn try_pop(&self) -> Option<(u32, u32, Vec<u8>)> {
        let m = &self.map;
        let mut pos = m.tail().load(Ordering::Relaxed);
        loop {
            let cell = pos & m.mask();
            let seq = m.seq(cell).load(Ordering::Acquire);
            let diff = seq.wrapping_sub(pos + 1) as i64;
            if diff == 0 {
                match m.tail().compare_exchange_weak(
                    pos,
                    pos + 1,
                    Ordering::AcqRel,
                    Ordering::Relaxed,
                ) {
                    Ok(_) => break,
                    Err(actual) => {
                        pos = actual;
                        continue;
                    }
                }
            } else if diff < 0 {
                if m.head().load(Ordering::Acquire) <= pos {
                    return None;
                }
                pos = m.tail().load(Ordering::Relaxed);
            } else {
                pos = m.tail().load(Ordering::Relaxed);
            }
        }
        let cell = pos & m.mask();
        unsafe {
            let slot = m.slot(cell);
            let kind = (slot as *const u32).read_unaligned();
            let cookie = (slot.add(4) as *const u32).read_unaligned();
            let mut len = (slot.add(8) as *const u32).read_unaligned() as usize;
            if len > MAX_PATH {
                len = MAX_PATH;
            }
            let mut path = vec![0u8; len];
            std::ptr::copy_nonoverlapping(slot.add(PATH_OFF), path.as_mut_ptr(), len);
            m.seq(cell).store(pos + m.slots, Ordering::Release);
            Some((kind, cookie, path))
        }
    }
}

impl Drop for Consumer {
    fn drop(&mut self) {
        unsafe { libc::close(self.fd) };
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ring_roundtrip() {
        let dir = std::env::temp_dir().join(format!("ebuild-ring-test-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let sock = dir.join("ring.dat");
        // Capacity must exceed total pushed items: producers spin (never
        // block) on Full, and the consumer drains only after they finish.
        let p = Producer::open(&sock, 2048).unwrap();
        let c = Consumer::open(&sock).unwrap();
        for i in 0..1000u32 {
            p.try_push(2, i, &format!("/src/file{}.c", i)).unwrap();
        }
        for i in 0..1000u32 {
            let (k, cookie, path) = c.try_pop().unwrap();
            assert_eq!((k, cookie), (2, i));
            assert_eq!(path, format!("/src/file{}.c", i).as_bytes());
        }
        assert!(c.try_pop().is_none());
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn ring_mpmc_concurrent() {
        use std::sync::Arc;
        let dir = std::env::temp_dir().join(format!("ebuild-ring-mt-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let sock = dir.join("ring.dat");
        // 4 producers × 500 items = 2000; capacity must exceed that because
        // Full spins (never blocks) and draining starts after join.
        let p = Arc::new(Producer::open(&sock, 4096).unwrap());
        let mut handles = Vec::new();
        for t in 0..4u32 {
            let p = p.clone();
            handles.push(std::thread::spawn(move || {
                for i in 0..500u32 {
                    loop {
                        match p.try_push(1, t * 1000 + i, "x") {
                            Ok(()) => break,
                            Err(PushError::Full) => std::thread::yield_now(),
                            Err(e) => panic!("{:?}", e),
                        }
                    }
                }
            }));
        }
        for h in handles {
            h.join().unwrap();
        }
        let c = Consumer::open(&sock).unwrap();
        let mut n = 0;
        while c.try_pop().is_some() {
            n += 1;
        }
        assert_eq!(n, 2000);
        let _ = std::fs::remove_dir_all(&dir);
    }
}
