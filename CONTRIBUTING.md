# Contributing to ebuild

## Prerequisites

- Go 1.24+ (`CGO_ENABLED=0` is mandatory — the `ci.yml` workflow rejects any
  cgo import).
- Rust stable (daemon builds with std only; `wasmtime` is an optional cargo
  feature).
- Lua 5.4 (`luac` must byte-compile every file in `rules/` cleanly).
- gcc/clang with `-std=c11 -Wall -Wextra -Werror`.

## Workflow

1. Fork, branch from `main`, keep changes focused.
2. Go: `gofmt -l .` must be empty; `go vet ./...` must pass.
3. C: compile `internal/daemon/worker.c` with the flags above, plus the
   `+Lua` variant (`-DEBUILD_HAVE_LUA=1`).
4. Lua rules: each rule must `return` a descriptor table with
   `name`, `params`, `validate`, and `expand`; only `table/string/math`
   libraries may be used (the sandbox strips the rest).
5. Add/extend tests alongside behavior changes; update `website/docs` when
   user-facing behavior changes.

## Review bar

- No placeholders, no mocked logic, no new external dependencies without a
  design note.
- All new IPC fields must be backward compatible (additive JSON fields only).
- Performance-sensitive paths need a benchmark note in the PR description.
