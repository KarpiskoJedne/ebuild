//! ebuild native daemon: kernel file watching, shm event ring, BLAKE3 CAS,
//! Wasmtime sandbox, and the UDS control plane shared with the Go
//! orchestrator (`internal/daemon/ipc_client.go`).
//!
//! Build: `cargo build --release -p ebuild-daemon`
//! Run:   `./target/release/ebuild-daemon --root . --socket /tmp/ebuild.sock`
//!
//! Control plane (unchanged JSON frame protocol): 4-byte big-endian length +
//! `{"id","method","params"}` over a Unix Domain Socket. Methods: `status`,
//! `watch`, `submit`, `poll`, `hash`, `run`, `run_wasm`, `gc`.
//! High-frequency file events additionally stream through the shared-memory
//! lock-free ring (`--shm` path) with zero syscalls on the consumer path.

mod cas;
mod fsmon;
mod sandbox;
mod shm_ring;

use std::collections::{HashMap, VecDeque};
use std::fs;
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

// ------------------------------------------------------------ JSON mini ----
fn jstr(s: &str) -> String {
    let mut o = String::with_capacity(s.len() + 2);
    o.push('"');
    for c in s.chars() {
        match c {
            '"' => o.push_str("\\\""),
            '\\' => o.push_str("\\\\"),
            '\n' => o.push_str("\\n"),
            c if (c as u32) < 0x20 => o.push_str(&format!("\\u{:04x}", c as u32)),
            c => o.push(c),
        }
    }
    o.push('"');
    o
}

/// Extract a top-level scalar field from a flat JSON object.
fn field(body: &str, key: &str) -> Option<String> {
    let pat = format!("\"{}\"", key);
    let mut i = body.find(&pat)?;
    i += pat.len();
    let rest = body[i..].trim_start();
    let rest = rest.strip_prefix(':')?.trim_start();
    if let Some(quoted) = rest.strip_prefix('"') {
        let mut out = String::new();
        let mut esc = false;
        for c in quoted.chars() {
            if esc {
                out.push(c);
                esc = false;
            } else if c == '\\' {
                esc = true;
            } else if c == '"' {
                return Some(out);
            } else {
                out.push(c);
            }
        }
        None
    } else {
        let end = rest.find([',', '}']).unwrap_or(rest.len());
        Some(rest[..end].trim().to_string())
    }
}

// ---------------------------------------------------------------- state ----
#[derive(Clone)]
struct Event {
    kind: u32,
    cookie: u32,
    path: String,
    hash: String,
    size: u64,
    mtime_ns: u64,
}

struct Daemon {
    roots: Vec<(PathBuf, bool)>,
    cas_dir: PathBuf,
    shm_path: PathBuf,
    queue: VecDeque<Event>,
    snap: HashMap<PathBuf, (u64, u64)>, // path -> (size, mtime_ns)
    task_seq: u64,
}

impl Daemon {
    fn push(&mut self, ring: &shm_ring::Producer, e: &Event) {
        if self.queue.len() >= 4096 {
            self.queue.pop_front();
            self.queue.push_back(Event {
                kind: 6,
                cookie: 0,
                path: String::new(),
                hash: String::new(),
                size: u64::MAX,
                mtime_ns: 0,
            });
        }
        self.queue.push_back(e.clone());
        // Best-effort mirror into shared memory; a full ring just drops.
        let _ = ring.try_push(e.kind, e.cookie, &e.path);
    }
}

const IGNORED: &[&str] = &[".git", ".hg", ".svn", "node_modules", "target", ".ebuild"];

fn mtime_ns(p: &Path) -> u64 {
    fs::metadata(p)
        .and_then(|m| m.modified())
        .ok()
        .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0)
}

fn walk_files(dir: &Path, recursive: bool, out: &mut Vec<PathBuf>) {
    let Ok(rd) = fs::read_dir(dir) else {
        return;
    };
    for e in rd.flatten() {
        let p = e.path();
        if p.is_dir() {
            if recursive
                && !p
                    .file_name()
                    .and_then(|s| s.to_str())
                    .map(|s| IGNORED.contains(&s))
                    .unwrap_or(false)
            {
                walk_files(&p, true, out);
            }
        } else {
            out.push(p);
        }
    }
}

/// Hash + CAS-ingest one changed file; returns (digest, size).
fn ingest(cas: &Path, path: &Path) -> Option<(String, u64)> {
    // Single read: sources are small; one pass feeds hash + store.
    let data = fs::read(path).ok()?;
    let size = data.len() as u64;
    let digest = cas::hash_bytes(&data);
    cas::cas_put(cas, &data).ok()?;
    Some((digest, size))
}

// ---------------------------------------------------------- sandbox exec ----
/// Native fallback for non-WASM commands: cleared env + allowlist, optional
/// `unshare -n` loopback-only network on Linux.
fn run_sandboxed(workdir: &str, argv: &[String], extra_env: &[String]) -> (i32, String) {
    if argv.is_empty() {
        return (127, "empty argv".into());
    }
    #[cfg(target_os = "linux")]
    let use_ns = std::process::Command::new("unshare")
        .arg("--help")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false);
    #[cfg(not(target_os = "linux"))]
    let use_ns = false;
    let mut cmd = if use_ns {
        let mut c = std::process::Command::new("unshare");
        c.arg("-n").arg("--").arg(&argv[0]);
        if argv.len() > 1 {
            c.args(&argv[1..]);
        }
        c
    } else {
        let mut c = std::process::Command::new(&argv[0]);
        if argv.len() > 1 {
            c.args(&argv[1..]);
        }
        c
    };
    cmd.env_clear();
    for kv in [
        "PATH",
        "HOME",
        "TMPDIR",
        "TEMP",
        "TZ",
        "LANG",
        "LC_ALL",
        "SYSTEMROOT",
    ] {
        if let Ok(v) = std::env::var(kv) {
            cmd.env(kv, v);
        }
    }
    for kv in extra_env {
        if let Some(i) = kv.find('=') {
            cmd.env(&kv[..i], &kv[i + 1..]);
        }
    }
    if !workdir.is_empty() {
        cmd.current_dir(workdir);
    }
    match cmd.output() {
        Ok(o) => {
            let mut log = String::from_utf8_lossy(&o.stdout).into_owned();
            log.push_str(&String::from_utf8_lossy(&o.stderr));
            (o.status.code().unwrap_or(-1), log)
        }
        Err(e) => (127, e.to_string()),
    }
}

// ------------------------------------------------------------------ IPC ----
fn event_json(e: &Event) -> String {
    format!(
        "{{\"kind\":{},\"cookie\":{},\"path\":{},\"hash\":{},\"size\":{},\"mtime_ns\":{}}}",
        e.kind,
        e.cookie,
        jstr(&e.path),
        jstr(&e.hash),
        e.size,
        e.mtime_ns
    )
}

fn handle(body: &str, state: &Arc<(Mutex<Daemon>, Condvar)>, ring: &shm_ring::Producer) -> String {
    let method = field(body, "method").unwrap_or_default();
    let (lock, cvar) = &**state;
    match method.as_str() {
        "status" => {
            let d = lock.lock().unwrap();
            let roots: Vec<String> = d
                .roots
                .iter()
                .map(|(r, _)| jstr(&r.to_string_lossy()))
                .collect();
            format!(
                "{{\"status\":{}}}",
                jstr(&format!(
                    "ok roots=[{}] queued={} shm={} ring_slots={}",
                    roots.join(","),
                    d.queue.len(),
                    d.shm_path.display(),
                    ring.slots()
                ))
            )
        }
        "watch" => {
            let path = field(body, "path").unwrap_or_default();
            let rec = field(body, "recursive")
                .map(|v| v == "true")
                .unwrap_or(true);
            let mut d = lock.lock().unwrap();
            d.roots.push((PathBuf::from(path), rec));
            "{\"ok\":true}".to_string()
        }
        "submit" => {
            let path = field(body, "path").unwrap_or_default();
            let mut d = lock.lock().unwrap();
            d.task_seq += 1;
            let tid = d.task_seq;
            let (mut h, mut size) = (String::new(), u64::MAX);
            if let Some((dig, n)) = ingest(&d.cas_dir.clone(), Path::new(&path)) {
                h = dig;
                size = n;
            }
            let e = Event {
                kind: 2,
                cookie: (tid & 0xFFFF_FFFF) as u32,
                path,
                hash: h,
                size,
                mtime_ns: 0,
            };
            d.push(ring, &e);
            cvar.notify_all();
            format!("{{\"id\":{}}}", tid)
        }
        "poll" => {
            let ms: u64 = field(body, "timeout_ms")
                .and_then(|v| v.parse().ok())
                .unwrap_or(0);
            let mut d = lock.lock().unwrap();
            if let Some(e) = d.queue.pop_front() {
                return event_json(&e);
            }
            if ms == 0 {
                return "{\"kind\":0,\"cookie\":0,\"path\":\"\",\"hash\":\"\",\"size\":0,\"mtime_ns\":0}".to_string();
            }
            let (d2, _) = cvar.wait_timeout(d, Duration::from_millis(ms)).unwrap();
            let mut d2 = d2;
            if let Some(e) = d2.queue.pop_front() {
                return event_json(&e);
            }
            "{\"kind\":0,\"cookie\":0,\"path\":\"\",\"hash\":\"\",\"size\":0,\"mtime_ns\":0}"
                .to_string()
        }
        "hash" => {
            let path = field(body, "path").unwrap_or_default();
            match cas::hash_file(Path::new(&path)) {
                Ok((h, _)) => {
                    let cached = cas::cas_has(&lock.lock().unwrap().cas_dir.clone(), &h);
                    format!("{{\"hash\":{},\"cached\":{}}}", jstr(&h), cached)
                }
                Err(_) => "{\"error\":\"hash: file not readable\"}".to_string(),
            }
        }
        "hash_many" => {
            // params: paths (comma-separated). Hashes in parallel via rayon.
            let list = field(body, "paths").unwrap_or_default();
            let paths: Vec<PathBuf> = list
                .split(',')
                .filter(|s| !s.is_empty())
                .map(PathBuf::from)
                .collect();
            let out = cas::hash_files_parallel(&paths);
            let mut items = Vec::with_capacity(out.len());
            for r in &out {
                match r {
                    Ok((p, h, n)) => items.push(format!(
                        "{{\"path\":{},\"hash\":{},\"size\":{}}}",
                        jstr(&p.to_string_lossy()),
                        jstr(h),
                        n
                    )),
                    Err(_) => items.push("{\"error\":\"unreadable\"}".to_string()),
                }
            }
            format!("{{\"items\":[{}]}}", items.join(","))
        }
        "run" => {
            let argv_s = field(body, "argv").unwrap_or_default();
            let workdir = field(body, "workdir").unwrap_or_default();
            let argv: Vec<String> = argv_s.split_whitespace().map(|s| s.to_string()).collect();
            let (code, log) = run_sandboxed(&workdir, &argv, &[]);
            format!("{{\"code\":{},\"log\":{}}}", code, jstr(&log))
        }
        "run_wasm" => {
            // params: path, argv (space-separated), env ("K=V,K=V"),
            // mem_mb, fuel, wall_secs.
            let path = field(body, "path").unwrap_or_default();
            let argv_s = field(body, "argv").unwrap_or_default();
            let env_s = field(body, "env").unwrap_or_default();
            let mem_mb: usize = field(body, "mem_mb")
                .and_then(|v| v.parse().ok())
                .unwrap_or(256);
            let fuel: u64 = field(body, "fuel")
                .and_then(|v| v.parse().ok())
                .unwrap_or(200_000_000);
            let wall_secs: u64 = field(body, "wall_secs")
                .and_then(|v| v.parse().ok())
                .unwrap_or(300);
            let argv: Vec<String> = argv_s.split_whitespace().map(|s| s.to_string()).collect();
            let env: Vec<(String, String)> = env_s
                .split(',')
                .filter_map(|kv| {
                    kv.find('=')
                        .map(|i| (kv[..i].to_string(), kv[i + 1..].to_string()))
                })
                .collect();
            let limits = sandbox::SandboxLimits {
                max_memory_bytes: mem_mb << 20,
                fuel,
                max_output_bytes: 8 << 20,
                wall_timeout: Duration::from_secs(wall_secs),
            };
            match sandbox::run_wasm(Path::new(&path), &argv, &env, &[], &limits) {
                Ok(o) => format!(
                    "{{\"code\":{},\"timed_out\":{},\"log\":{},\"err\":{}}}",
                    o.exit_code,
                    o.timed_out,
                    jstr(&String::from_utf8_lossy(&o.stdout)),
                    jstr(&String::from_utf8_lossy(&o.stderr))
                ),
                Err(sandbox::SandboxError::Timeout) => {
                    "{\"error\":\"wasm execution timed out\"}".to_string()
                }
                Err(e) => format!("{{\"error\":{}}}", jstr(&e.to_string())),
            }
        }
        "gc" => {
            let max_mb: u64 = field(body, "max_mb")
                .and_then(|v| v.parse().ok())
                .unwrap_or(10240);
            let cas = lock.lock().unwrap().cas_dir.clone();
            let (freed, removed) = cas::gc(&cas, max_mb * 1024 * 1024);
            format!("{{\"freed\":{},\"removed\":{}}}", freed, removed)
        }
        _ => "{\"error\":\"unknown method\"}".to_string(),
    }
}

fn serve_conn(
    stream: UnixStream,
    state: Arc<(Mutex<Daemon>, Condvar)>,
    ring: Arc<shm_ring::Producer>,
) {
    let mut s = stream;
    loop {
        let mut lenb = [0u8; 4];
        if s.read_exact(&mut lenb).is_err() {
            return;
        }
        let n = u32::from_be_bytes(lenb) as usize;
        if n == 0 || n > 64 * 1024 * 1024 {
            return;
        }
        let mut buf = vec![0u8; n];
        if s.read_exact(&mut buf).is_err() {
            return;
        }
        let body = String::from_utf8_lossy(&buf).into_owned();
        let id = field(&body, "id").unwrap_or("0".into());
        let result = handle(&body, &state, &ring);
        let payload = if result.contains("\"error\"") && !result.contains("\"kind\"") {
            format!(
                "{{\"id\":{},\"result\":null,\"error\":{}}}",
                id,
                jstr(&result)
            )
        } else {
            format!("{{\"id\":{},\"result\":{},\"error\":\"\"}}", id, result)
        };
        let len = (payload.len() as u32).to_be_bytes();
        if s.write_all(&len).is_err() || s.write_all(payload.as_bytes()).is_err() {
            return;
        }
    }
}

fn p2p_server(cas: PathBuf) {
    let listener = match std::net::TcpListener::bind("127.0.0.1:44732") {
        Ok(l) => l,
        Err(_) => return,
    };
    for conn in listener.incoming().flatten() {
        let cas = cas.clone();
        std::thread::spawn(move || {
            let mut c = conn;
            let mut req = [0u8; 72];
            if c.read_exact(&mut req).is_err() {
                return;
            }
            let hash = String::from_utf8_lossy(&req[..64]).into_owned();
            if hash.len() != 64 || !hash.bytes().all(|b| b.is_ascii_hexdigit()) {
                return;
            }
            // Serve decompressed bytes (wire format = raw content).
            if let Ok(data) = cas::cas_get(&cas, &hash, None) {
                let _ = c.write_all(&data);
            }
        });
    }
}

fn default_shm_path() -> PathBuf {
    #[cfg(target_os = "linux")]
    {
        let uid = unsafe { libc::getuid() };
        PathBuf::from(format!("/dev/shm/ebuild-ring-{}.dat", uid))
    }
    #[cfg(not(target_os = "linux"))]
    {
        std::env::temp_dir().join(format!("ebuild-ring-{}.dat", std::process::id()))
    }
}

fn print_usage() {
    eprintln!("usage: ebuild-daemon --root DIR [--socket PATH] [--cas DIR] [--shm PATH]");
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let mut root = String::from(".");
    let mut socket = format!("{}/ebuild.sock", std::env::temp_dir().to_string_lossy());
    let mut cas = String::new();
    let mut shm = default_shm_path();
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--root" if i + 1 < args.len() => {
                root = args[i + 1].clone();
                i += 2;
            }
            "--socket" if i + 1 < args.len() => {
                socket = args[i + 1].clone();
                i += 2;
            }
            "--cas" if i + 1 < args.len() => {
                cas = args[i + 1].clone();
                i += 2;
            }
            "--shm" if i + 1 < args.len() => {
                shm = PathBuf::from(&args[i + 1]);
                i += 2;
            }
            "-h" | "--help" => {
                print_usage();
                return;
            }
            _ => {
                print_usage();
                std::process::exit(2);
            }
        }
    }
    if cas.is_empty() {
        cas = format!("{}/.ebuild/cas", root);
    }
    let _ = fs::create_dir_all(&cas);

    let ring = Arc::new(match shm_ring::Producer::open(&shm, 4096) {
        Ok(r) => r,
        Err(e) => {
            eprintln!("ebuild-daemon: shm ring {}: {}", shm.display(), e);
            std::process::exit(1);
        }
    });

    // Seed snapshot from metadata (hash lazily on change, not at startup).
    let mut snap = HashMap::new();
    let mut seed: Vec<PathBuf> = Vec::new();
    walk_files(Path::new(&root), true, &mut seed);
    for p in seed {
        let size = fs::metadata(&p).map(|m| m.len()).unwrap_or(0);
        snap.insert(p.clone(), (size, mtime_ns(&p)));
    }
    let state: Arc<(Mutex<Daemon>, Condvar)> = Arc::new((
        Mutex::new(Daemon {
            roots: vec![(PathBuf::from(&root), true)],
            cas_dir: PathBuf::from(&cas),
            shm_path: shm.clone(),
            queue: VecDeque::new(),
            snap,
            task_seq: 0,
        }),
        Condvar::new(),
    ));

    // Kernel-native watcher → queue + shm ring + CAS ingest.
    let watcher = fsmon::Watcher::new().unwrap_or_else(|e| {
        eprintln!("ebuild-daemon: file watcher: {}", e);
        std::process::exit(1);
    });
    for (r, rec) in state.0.lock().unwrap().roots.clone() {
        let _ = watcher.watch(&r, rec);
    }
    {
        let state = state.clone();
        let ring = ring.clone();
        std::thread::Builder::new()
            .name("ebuild-ingest".into())
            .spawn(move || {
                // NOTE: recv_timeout returns None on every quiet interval;
                // that is normal idle, NOT shutdown — keep waiting.
                loop {
                    let Some(ev) = watcher.recv_timeout(Duration::from_millis(500)) else {
                        continue;
                    }; // `watcher` must stay alive here: dropping it would
                       // disconnect the backend thread's event channel.
                       // Re-stat: kernels report the event before writes settle;
                       // a settled size+mtime pair is what we snapshot.
                    let size = fs::metadata(&ev.path).map(|m| m.len()).unwrap_or(0);
                    let mt = mtime_ns(&ev.path);
                    let (lock, cvar) = &*state;
                    let mut d = lock.lock().unwrap();
                    let changed = match ev.kind {
                        1 => {
                            // Created: ingest unless we already have it.
                            if d.snap.contains_key(&ev.path) {
                                None
                            } else {
                                ingest(&d.cas_dir.clone(), &ev.path).or(Some((String::new(), size)))
                            }
                        }
                        2 | 4 | 5 => match d.snap.get(&ev.path) {
                            Some((os, om)) if os == &size && om == &mt => None,
                            _ => {
                                ingest(&d.cas_dir.clone(), &ev.path).or(Some((String::new(), size)))
                            }
                        },
                        3 => {
                            d.snap.remove(&ev.path);
                            Some((String::new(), u64::MAX))
                        }
                        6 => Some((String::new(), u64::MAX)),
                        _ => None,
                    };
                    if let Some((h, n)) = changed {
                        if ev.kind != 3 {
                            d.snap.insert(ev.path.clone(), (size, mt));
                        }
                        let e = Event {
                            kind: ev.kind,
                            cookie: ev.cookie,
                            path: ev.path.to_string_lossy().into_owned(),
                            hash: h,
                            size: n,
                            mtime_ns: if ev.kind == 3 { 0 } else { mt },
                        };
                        d.push(&ring, &e);
                        cvar.notify_all();
                    }
                }
            })
            .expect("ingest thread");
    }
    {
        let casp = PathBuf::from(&cas);
        std::thread::spawn(move || p2p_server(casp));
    }
    let _ = fs::remove_file(&socket);
    let listener = match UnixListener::bind(&socket) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("ebuild-daemon: bind {}: {}", socket, e);
            std::process::exit(1);
        }
    };
    eprintln!("ebuild-daemon: root watching, socket {}", socket);
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    eprintln!("ebuild-daemon: started at {} shm={}", now, shm.display());
    for conn in listener.incoming() {
        match conn {
            Ok(s) => {
                let st = state.clone();
                let r = ring.clone();
                std::thread::spawn(move || serve_conn(s, st, r));
            }
            Err(_) => break,
        }
    }
}
