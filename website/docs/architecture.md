# Architecture

## Hybrid daemon + orchestrator

```text
┌──────────────┐  UDS/Named-Pipe JSON frames   ┌──────────────────┐
│ Go CLI/orch. │ ◄──────────────────────────► │  native daemon   │
│ CGO_ENABLED=0│  + shm ring (hot events)      │  C11 + Rust      │
└──────────────┘                               └──────────────────┘
```

## Module map

| Path | Role |
| ---- | ---- |
| `cmd/ebuild/main.go` | CLI (`build/clean/daemon/query/migrate`) |
| `internal/core/scheduler.go` | adaptive workers, backpressure, JSONL tracing |
| `internal/core/toolchain.go` | language detection, SHA-256 verify, hermetic env |
| `internal/core/asdf_adapter.go` | `.tool-versions` pins → asdf installs |
| `internal/core/config_parser.go` | `ebuild.yml` → targets → topological plan |
| `internal/core/reapi.go` | CAS sync (digest model) + predictive prefetch |
| `internal/core/query.go` | deps/rdeps/critical-path/impact |
| `internal/core/tui.go` | ANSI live dashboard (Gantt, CPU, cache) |
| `internal/core/migrate.go` | Bazel/CMake/Make/Python → `ebuild.yml` |
| `internal/core/sbom.go` | CycloneDX/SPDX + SLSA/cosign attestation |
| `internal/core/lsp.go` | stdio LSP client + symbol→target mapping |
| `internal/core/ai_predict.go` | logistic failure model + scheduling hints |
| `internal/daemon/ipc_client.go` | zero-CGO IPC client |
| `internal/daemon/p2p.go` | mDNS discovery + TCP CAS fetch |
| `internal/daemon/garbage.go` | LRU CAS garbage collection |
| `internal/daemon/worker.c` | C11 watcher/hash/Lua-sandbox core |
| `internal/daemon/worker_host.rs` | Rust daemon: watch, CAS, sandbox, IPC, P2P |
| `rules/01–50_*.lua` | 50 sandboxed build rules |

## IPC protocol

Frames are `u32be length + JSON`. Requests carry `{id, method, params}`;
responses carry `{id, result, error}`. Methods: `status`, `watch`,
`submit`, `poll`, `hash`, `run`, `gc`. Fields are additive-only.

## Sandboxing

Actions run with a cleared environment (allowlist + declared extras) and
loopback-only networking (Linux `unshare -n`, Windows Job Objects,
macOS Seatbelt — see `SECURITY.md`).

## Caching

Artifacts are keyed by content hash. Peers on the LAN share blobs via
mDNS discovery (`_ebuild-cas._tcp.local`) and TCP fetch; cold entries are
pulled from the remote CAS with graph-neighbor prefetch. LRU collection
bounds disk usage.
