# ebuild — Easy Build

`ebuild` is an enterprise-grade, universal, zero-configuration build system.
You declare **what** to build in a few lines of `ebuild.yml`; the system
discovers languages, pins toolchains, watches the workspace, and produces
bit-for-bit reproducible artifacts backed by content-addressed storage.

## Architecture (hybrid, zero CGO)

- **Native daemon** (`internal/daemon/`, Rust + Cargo): kernel file watchers
  (inotify / FSEvents / ReadDirectoryChangesW, zero polling), lock-free
  shared-memory event ring, parallel BLAKE3 CAS (rayon), Wasmtime WASM
  sandbox, plus the C11 core (`worker.c`) with Lua plan evaluation.
- **Go orchestrator** (`CGO_ENABLED=0`, stdlib only): CLI, scheduler,
  toolchain + asdf manager, `ebuild.yml` parser, REAPI client, graph queries,
  TUI, migration engine, SBOM/SLSA attestation, LSP hooks, AI heuristics.
- **IPC**: Go talks to the daemon over Unix Domain Sockets (Linux/macOS) or
  Named Pipes (Windows) with a length-prefixed JSON frame protocol, plus a
  shared-memory ring for high-frequency events. No CGO anywhere in Go.
- **Rules**: 50 production Lua rules in `rules/` (compile, test, package,
  lint, deploy) executed inside the daemon sandbox.

## Quick start

```sh
# build the CLI (no CGO, no network needed — stdlib only)
CGO_ENABLED=0 go build ./...

# start the daemon (Rust + Cargo: kernel watchers, shm ring, BLAKE3 CAS, Wasmtime)
cargo build --release --manifest-path internal/daemon/Cargo.toml
./target/release/ebuild-daemon --root . --socket /tmp/ebuild.sock &

# write a minimal ebuild.yml skeleton, then edit it by hand
./ebuild init

# build everything declared in ebuild.yml (outputs land in .ebuild/)
./ebuild build all    # (same as //...)

# build + execute a binary target (like `bazel run`)
./ebuild run //app:svc -- --port 8080
./ebuild run all      # every runnable binary, in dependency order

# run a custom command from the `commands:` section of ebuild.yml
./ebuild runc test -- ./svc/...

# built-ins (no config needed, sandboxed to the workspace)
./ebuild runc copy app/hello.c backup/
./ebuild runc clean
./ebuild runc delete backup/

# live dashboard
./ebuild daemon --tui

# migrate a legacy project
./ebuild migrate --from=cmake --root /path/to/project
```

## Layout

See `website/docs/architecture.md` for the full module map and
`website/docs/getting-started.md` for the tutorial.

## Security

See `SECURITY.md`. Builds run hermetically: loopback-only network, sanitized
environment, overlay/chroot inputs, SLSA provenance + CycloneDX/SPDX SBOM per
target.

## License

Apache-2.0 — see `LICENSE`.
# ebuild
