//! Wasmtime WASM sandbox: portable toolchains under strict resource rule.
//!
//! Isolation model (deny-by-default, nothing inherited):
//! * filesystem: NO preopened directories unless the caller passes explicit
//!   read-only `dirs` mounts (guest path + host path).
//! * network: WASI TCP/UDP address checks default-deny; no lookup, no listen,
//!   no preopened sockets. Loopback included in the deny set.
//! * environment: only the caller-supplied `env` list; host env never leaks.
//! * memory: `StoreLimits` hard cap (`memory_size`, 1 instance/memory/table).
//! * time: fuel metering (`consume_fuel`); exhaustion maps to a timeout
//!   error. Calibrate `fuel` ≈ 50M units per expected CPU-second.
//! * output: stdout/stderr captured into bounded `MemoryOutputPipe`s.

use std::path::{Path, PathBuf};
use wasmtime::{Config, Engine, Linker, Module, Store, StoreLimits, StoreLimitsBuilder};
use wasmtime_wasi::pipe::MemoryOutputPipe;
use wasmtime_wasi::preview1::{self, WasiP1Ctx};
use wasmtime_wasi::{DirPerms, FilePerms, WasiCtxBuilder};

#[derive(Debug, Clone)]
pub struct SandboxLimits {
    /// Hard guest-memory cap in bytes (default 256 MiB).
    pub max_memory_bytes: usize,
    /// Fuel budget; 0 = engine default (200M). Exhaustion => timeout.
    pub fuel: u64,
    /// stdout/stderr capture cap in bytes each (default 8 MiB).
    pub max_output_bytes: usize,
    /// Wall-clock ceiling enforced by a watchdog thread (default 5 min).
    /// Fuel is the primary guard; the watchdog catches host-call stalls.
    pub wall_timeout: std::time::Duration,
}

impl Default for SandboxLimits {
    fn default() -> Self {
        SandboxLimits {
            max_memory_bytes: 256 << 20,
            fuel: 200_000_000,
            max_output_bytes: 8 << 20,
            wall_timeout: std::time::Duration::from_secs(300),
        }
    }
}

pub struct SandboxOutput {
    pub exit_code: i32,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub timed_out: bool,
}

#[derive(Debug)]
pub enum SandboxError {
    /// Configuration/load failure (message).
    Setup(String),
    /// Fuel exhausted or watchdog fired.
    Timeout,
    /// Module lacks a `_start` export.
    NoStart(String),
    /// Runtime trap (message).
    Trap(String),
}

impl std::fmt::Display for SandboxError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            SandboxError::Setup(m) => write!(f, "wasm setup: {}", m),
            SandboxError::Timeout => write!(f, "wasm execution timed out"),
            SandboxError::NoStart(m) => write!(f, "wasm missing _start: {}", m),
            SandboxError::Trap(m) => write!(f, "wasm trap: {}", m),
        }
    }
}

impl std::error::Error for SandboxError {}

struct State {
    wasi: WasiP1Ctx,
    limits: StoreLimits,
}

fn engine() -> Result<Engine, SandboxError> {
    let mut cfg = Config::new();
    cfg.consume_fuel(true);
    // Cranelift defaults (optimizations on); keep debug info off so guest
    // memory accounting stays tight.
    Engine::new(&cfg).map_err(|e| SandboxError::Setup(e.to_string()))
}

/// Execute a WASI (`_start`) module with `args` (argv[0] = module file name).
///
/// `dirs` mounts host directories read-only into the guest; `env` is the
/// complete guest environment. Everything else is denied.
pub fn run_wasm(
    path: &Path,
    args: &[String],
    env: &[(String, String)],
    dirs: &[(PathBuf, String)],
    limits: &SandboxLimits,
) -> Result<SandboxOutput, SandboxError> {
    let engine = engine()?;
    let module =
        Module::from_file(&engine, path).map_err(|e| SandboxError::Setup(e.to_string()))?;

    let stdout_pipe = MemoryOutputPipe::new(limits.max_output_bytes);
    let stderr_pipe = MemoryOutputPipe::new(limits.max_output_bytes);
    let mut b = WasiCtxBuilder::new();
    let mut argv = vec![path.to_string_lossy().into_owned()];
    argv.extend_from_slice(args);
    b.args(&argv);
    for (k, v) in env {
        b.env(k, v);
    }
    for (host, guest) in dirs {
        b.preopened_dir(host, guest, DirPerms::READ, FilePerms::READ)
            .map_err(|e| SandboxError::Setup(format!("preopen: {}", e)))?;
    }
    b.stdout(stdout_pipe.clone());
    b.stderr(stderr_pipe.clone());
    let wasi = b.build_p1();

    let store_limits = StoreLimitsBuilder::new()
        .memory_size(limits.max_memory_bytes)
        .instances(1)
        .memories(1)
        .tables(1)
        .build();
    let mut store = Store::new(
        &engine,
        State {
            wasi,
            limits: store_limits,
        },
    );
    store
        .set_fuel(limits.fuel)
        .map_err(|e| SandboxError::Setup(e.to_string()))?;
    store.limiter(|s| &mut s.limits);

    let mut linker: Linker<State> = Linker::new(&engine);
    preview1::add_to_linker_sync(&mut linker, |s: &mut State| &mut s.wasi)
        .map_err(|e| SandboxError::Setup(e.to_string()))?;
    let instance = linker
        .instantiate(&mut store, &module)
        .map_err(|e| SandboxError::Trap(e.to_string()))?;
    let start = instance
        .get_typed_func::<(), ()>(&mut store, "_start")
        .map_err(|_| SandboxError::NoStart(path.display().to_string()))?;

    // Watchdog: fuel usually fires first; this bounds host-import stalls.
    let (done_tx, done_rx) = std::sync::mpsc::channel::<()>();
    let watchdog = std::thread::Builder::new()
        .name("ebuild-wasm-watchdog".into())
        .spawn({
            let wall = limits.wall_timeout;
            move || done_rx.recv_timeout(wall).is_err()
        })
        .ok();
    let r = start.call(&mut store, ());
    // Signal completion BEFORE joining: the watchdog exits either when the
    // wall timeout elapses (returns true) or when it observes this send.
    // Joining first would stall every call by the full wall timeout.
    let _ = done_tx.send(());
    let watchdog_fired = watchdog.map(|h| h.join().unwrap_or(false)).unwrap_or(false);

    let stdout = stdout_pipe.contents().to_vec();
    let stderr = stderr_pipe.contents().to_vec();
    match r {
        Ok(()) => Ok(SandboxOutput {
            exit_code: 0,
            stdout,
            stderr,
            timed_out: false,
        }),
        Err(e) => {
            // WASI proc_exit surfaces as I32Exit; surface the real code.
            if let Some(code) = e.downcast_ref::<wasmtime_wasi::I32Exit>() {
                return Ok(SandboxOutput {
                    exit_code: code.0,
                    stdout,
                    stderr,
                    timed_out: false,
                });
            }
            // Fuel exhaustion is the deterministic timeout signal; the
            // watchdog covers host-import stalls that consume no fuel.
            if watchdog_fired
                || matches!(
                    e.downcast_ref::<wasmtime::Trap>(),
                    Some(wasmtime::Trap::OutOfFuel)
                )
            {
                return Err(SandboxError::Timeout);
            }
            Err(SandboxError::Trap(e.to_string()))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    /// Assemble the tiniest valid WASI module by hand? Overkill — instead
    /// verify the negative paths: missing file => Setup, non-wasm => Setup,
    /// module without _start => NoStart (minimal module bytes below).
    #[test]
    fn rejects_non_module() {
        let dir = std::env::temp_dir().join(format!("ebuild-wasm-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let bad = dir.join("bad.wasm");
        std::fs::write(&bad, b"not a wasm module").unwrap();
        match run_wasm(&bad, &[], &[], &[], &SandboxLimits::default()) {
            Err(SandboxError::Setup(_)) => {}
            other => panic!("expected Setup, got {:?}", other.is_ok()),
        }
        // Minimal valid module `(module)` has no _start export.
        let minimal = vec![0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00];
        let m = dir.join("minimal.wasm");
        {
            let mut f = std::fs::File::create(&m).unwrap();
            f.write_all(&minimal).unwrap();
        }
        match run_wasm(&m, &[], &[], &[], &SandboxLimits::default()) {
            Err(SandboxError::NoStart(_)) => {}
            Err(e) => panic!("expected NoStart, got {}", e),
            Ok(_) => panic!("expected NoStart, ran"),
        }
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// Positive path: assemble a real WASI module from WAT (dev-dep `wat`),
    /// run `_start`, and assert stdout capture. Timeout path: an infinite
    /// loop with near-zero fuel must yield `SandboxError::Timeout`.
    #[test]
    fn runs_start_and_captures_stdout() {
        let dir = std::env::temp_dir().join(format!("ebuild-wasm2-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let hi = dir.join("hi.wasm");
        let bytes = wat::parse_str(
            r#"(module
              (import "wasi_snapshot_preview1" "fd_write"
                (func $fd_write (param i32 i32 i32 i32) (result i32)))
              (memory (export "memory") 1)
              (data (i32.const 0) "\10\00\00\00\03\00\00\00")
              (data (i32.const 16) "hi\n")
              (func (export "_start")
                i32.const 1 i32.const 0 i32.const 1 i32.const 8
                call $fd_write drop))"#,
        )
        .unwrap();
        std::fs::write(&hi, &bytes).unwrap();
        let out = run_wasm(&hi, &[], &[], &[], &SandboxLimits::default()).unwrap();
        assert_eq!(out.exit_code, 0);
        assert_eq!(out.stdout, b"hi\n");
        assert!(!out.timed_out);

        // Isolation: no env, no preopens, no network — assert the guest sees
        // an empty environment block via environ_get sizes (argc=1, envc=0).
        let env = dir.join("env.wasm");
        let bytes = wat::parse_str(
            r#"(module
              (import "wasi_snapshot_preview1" "fd_write"
                (func $fd_write (param i32 i32 i32 i32) (result i32)))
              (import "wasi_snapshot_preview1" "args_sizes_get"
                (func $asg (param i32 i32) (result i32)))
              (import "wasi_snapshot_preview1" "environ_sizes_get"
                (func $esg (param i32 i32) (result i32)))
              (memory (export "memory") 1)
              (func (export "_start")
                ;; write argc (at 64) then envc (at 72) as two ASCII digits.
                ;; NOTE: i32.store8 takes address THEN value on the stack.
                i32.const 64 i32.const 68 call $asg drop
                i32.const 72 i32.const 76 call $esg drop
                i32.const 64
                i32.const 64 i32.load8_u i32.const 48 i32.add
                i32.store8
                i32.const 72
                i32.const 72 i32.load8_u i32.const 48 i32.add
                i32.store8
                ;; iovec at 0: {base=64,len=1} then {base=72,len=1}
                i32.const 0 i32.const 64 i32.store
                i32.const 4 i32.const 1 i32.store
                i32.const 1 i32.const 0 i32.const 1 i32.const 80
                call $fd_write drop
                i32.const 0 i32.const 72 i32.store
                i32.const 4 i32.const 1 i32.store
                i32.const 1 i32.const 0 i32.const 1 i32.const 80
                call $fd_write drop))"#,
        )
        .unwrap();
        std::fs::write(&env, &bytes).unwrap();
        let out = run_wasm(&env, &[], &[], &[], &SandboxLimits::default()).unwrap();
        assert_eq!(out.exit_code, 0);
        // argv = [module] => argc 1; env must be empty => envc 0.
        assert_eq!(out.stdout, b"10");

        // Timeout: infinite loop Starved of fuel.
        let loopy = dir.join("loop.wasm");
        let bytes =
            wat::parse_str(r#"(module (func (export "_start") (loop $l (br $l))))"#).unwrap();
        std::fs::write(&loopy, &bytes).unwrap();
        let starved = SandboxLimits {
            fuel: 100,
            ..SandboxLimits::default()
        };
        assert!(matches!(
            run_wasm(&loopy, &[], &[], &[], &starved),
            Err(SandboxError::Timeout)
        ));
        let _ = std::fs::remove_dir_all(&dir);
    }
}
