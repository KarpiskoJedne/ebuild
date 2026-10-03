# Getting started with ebuild

## Install

```sh
# CLI (Go, no CGO)
CGO_ENABLED=0 go build -o ebuild ./cmd/ebuild

# Native daemon (Rust, std-only)
rustc --edition 2021 -O internal/daemon/worker_host.rs -o ebuild-daemon
```

## Your first build

Create `ebuild.yml` at the repository root:

```yaml
version: 1
project:
  name: demo
  root: .
targets:
  - name: //app:hello
    rule: cc_binary
    srcs: ["app/hello.c"]
```

Start the daemon and build:

```sh
./ebuild-daemon --root . --socket /tmp/ebuild.sock &
./ebuild build //...
```

## How it works

1. The daemon watches the workspace (inotify/kqueue/FSEvents/
   ReadDirectoryChangesW) and hashes changed files (mmap + BLAKE3/SHA-256).
2. The Go orchestrator parses `ebuild.yml` into a dependency graph, pins
   toolchains via asdf/`.tool-versions`, and schedules actions.
3. Language rules in `rules/` expand each target into sandboxed actions;
   artifacts land in content-addressed storage with SBOM + SLSA provenance.

## Next steps

- `ebuild query deps //app:hello` — inspect the graph.
- `ebuild daemon --tui` — live Gantt dashboard.
- `ebuild migrate --from=cmake --root ~/legacy` — import an old project.

## Starting from scratch: ebuild init

No `ebuild.yml` yet? Write a minimal skeleton and fill it in by hand:

```sh
ebuild init                # writes ebuild.yml (refuses to overwrite)
ebuild init --force        # overwrite an existing ebuild.yml
```

```yaml
version: 1

project:
  name: demo
  root: .

targets:
  - name: //app:hello
    rule: cc_binary        # 50 rules: cc_binary, go_binary, python_package, ...
    srcs: ["app/hello.c"]
    # hdrs: ["app/hello.h"]
    # deps: ["//lib:base"]
    # out: .ebuild/bin/hello
    # toolchain: clang    # backend variant; empty = first available

commands:
  - name: test
    cmd: go test ./...
```

For each detected toolchain the form shows the installed version and, when
an asdf plugin is mapped (`go`→`golang`, `cargo`→`rust`, `python3`→`python`,
`node`→`nodejs`, …), lets you pick an installed version or install a new one
via `asdf install` on the spot. On a terminal the picker uses ↑↓/jk +
Enter (q cancels); otherwise a numbered menu is offered.

## Custom commands

Anything that is not a build target — tests, linters, scripts — goes into
the `commands:` section and runs hermetically (sanitized env, live stdio,
real exit codes):

```yaml
commands:
  - name: test
    cmd: go test ./...
```

```sh
./ebuild runc test -- ./svc/...
./ebuild runc --root ~/other test
```

## Toolchains: pick or fall back

Each target accepts `toolchain:` to select the backend
(`cc|gcc|clang|tcc|cl`, `cargo|rustc`, `uv|pip|poetry`,
`pytest|unittest`, `trivy|grype|semgrep`, `docker|podman`, …).
With no `toolchain:` set, ebuild uses the first backend found on PATH
(`pytest` → stdlib `unittest`, `uv` → `pip` → `poetry`, …), so plans work
on machines missing the preferred tool. Unknown toolchains fail fast with
the valid set instead of cryptic fork/exec errors.

```yaml
targets:
  - name: //app:svc
    rule: cc_binary
    toolchain: clang
    srcs: ["app/main.c"]
```

`ebuild init` offers the same choice interactively (arrow picker on a TTY,
numbered menu otherwise), backed by asdf version lookup. Rule selection in
`init` works the same way: all 50 rules with one-line help, arrows or
numbers, so nothing needs memorizing.

## Running binaries, version, doctor

```sh
./ebuild run //app:svc -- --port 8080  # build + execute a binary target
./ebuild run //app                     # package shorthand for //app:app
./ebuild version                       # CLI version
./ebuild doctor                        # toolchains, daemon, config, disk
./ebuild help run                      # details for any command
```

Build failures print the compiler/test log under the `FAIL` line and exit
non-zero; `run` never launches a binary that failed to build.

> Tip: `ebuild run` executes *commands*. For build targets use
> `ebuild build //pkg:name` — mixing them up prints a hint.

## Built-in commands

`copy`, `clean`, and `delete` work without any config and stay inside the
workspace (path escapes are refused):

```sh
./ebuild run copy app/hello.c backup/
./ebuild run clean            # removes .ebuild/ outputs
./ebuild run delete backup/
./ebuild run                  # lists commands + built-ins, pick on a TTY
```
