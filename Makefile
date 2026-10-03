# ebuild Makefile — build, test, and install the CLI + native daemon.
#
# Usage:
#   make                  # build everything
#   make install          # build (if needed) + copy binaries to $(PREFIX)/bin.
#                         # Install NEVER executes the binaries — it only copies them.
#   PREFIX=$HOME/.local make install   # unprivileged install (adds to PATH)
#   make uninstall        # remove installed binaries
#   make test             # go tests + rust tests + lua rule syntax check
#   make clean            # remove build artifacts
#
# Env knobs: PREFIX, DESTDIR, CARGO_PROFILE (release default), GOFLAGS,
# CARGO_TARGET_DIR (honored when locating the daemon binary).

PREFIX ?= /usr/local
DESTDIR ?=
BINDIR = $(DESTDIR)$(PREFIX)/bin

GO ?= go
CARGO ?= cargo
LUAC ?= luac

# Toolchain auto-detection. `GO`/`CARGO` may be overridden on the command
# line (GO=/opt/go/bin/go CARGO=$HOME/.cargo/bin/cargo); otherwise we look
# on PATH first, then in the standard install locations. This also covers
# `sudo make ...`, which resets PATH: when SUDO_USER is set we additionally
# probe that user's $HOME/.cargo/bin.
ifdef SUDO_USER
  INVOKER_HOME := $(shell echo ~$(SUDO_USER) 2>/dev/null)
  ifeq ($(INVOKER_HOME),)
    INVOKER_HOME = /home/$(SUDO_USER)
  endif
  ifeq (~$(SUDO_USER),$(INVOKER_HOME))
    INVOKER_HOME = /home/$(SUDO_USER)
  endif
else
  INVOKER_HOME = $(HOME)
endif
# Explicit command-line overrides (make GO=/path CARGO=/path) are honored
# as-is and fail loudly if bogus; otherwise we auto-detect.
ifeq ($(origin GO),command line)
  GO_BIN := $(GO)
else
  GO_BIN := $(shell command -v $(GO) 2>/dev/null)
  ifeq ($(GO_BIN),)
    GO_BIN := $(firstword $(wildcard /usr/local/go/bin/go $(HOME)/go/bin/go $(INVOKER_HOME)/go/bin/go))
  endif
endif
ifeq ($(origin CARGO),command line)
  CARGO_BIN := $(CARGO)
else
  CARGO_BIN := $(shell command -v $(CARGO) 2>/dev/null)
  ifeq ($(CARGO_BIN),)
    CARGO_BIN := $(firstword $(wildcard $(HOME)/.cargo/bin/cargo $(INVOKER_HOME)/.cargo/bin/cargo))
  endif
endif

export CGO_ENABLED = 0

CARGO_PROFILE ?= release
CARGO_FLAGS = --manifest-path internal/daemon/Cargo.toml
# Cargo maps the dev profile to target/debug; every other profile name maps
# to a same-named directory.
PROFILE_DIR = $(if $(filter release,$(CARGO_PROFILE)),release,debug)
ifeq ($(CARGO_PROFILE),release)
  CARGO_FLAGS += --release
else ifneq ($(CARGO_PROFILE),dev)
  CARGO_FLAGS += --profile $(CARGO_PROFILE)
endif
TARGET_DIR ?= $(shell echo $${CARGO_TARGET_DIR:-target})

# Privilege drop: `sudo make install` runs as root, but root usually has no
# rustup toolchain (and root-built artifacts would poison the user's cache).
# So the cargo build runs as the invoking user; only the final `install -d`
# / `install -m` steps keep root. Plain `make` / user builds are unaffected.
ifeq ($(shell id -u 2>/dev/null),0)
  ifdef SUDO_USER
    # Forward the effective target dir (absolute, so cwd changes can't break
    # it) plus the toolchain env; the `cp` below uses the same TARGET_DIR.
    _CARGO_ENV = env HOME="$(INVOKER_HOME)" CARGO_HOME="$(INVOKER_HOME)/.cargo" RUSTUP_HOME="$(INVOKER_HOME)/.rustup" CARGO_TARGET_DIR="$(abspath $(TARGET_DIR))" PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$(INVOKER_HOME)/.cargo/bin"
    _RUN_AS_USER = su -s /bin/sh $(SUDO_USER) -c
  endif
endif

CLI_BIN = ebuild
DAEMON_OUT = ebuild-daemon

.PHONY: all build cli daemon install uninstall test test-go test-rust test-lua clean help check-go check-cargo

all: build

build: cli daemon

check-go:
	@test -x "$(GO_BIN)" || (echo "error: Go toolchain not found (GO=$(GO_BIN))." >&2; \
	  echo "  install from https://go.dev/dl/ (needs Go >= 1.24)," >&2; \
	  echo "  or point at it explicitly:  make GO=/path/to/go build" >&2; \
	  echo '  under sudo, PATH is reset — run instead:  sudo env PATH="$$PATH" make install' >&2; \
	  echo '  or skip root entirely:  PREFIX=$$HOME/.local make install' >&2; \
	  exit 1)
	@echo "go: $(GO_BIN)"

check-cargo:
	@test -x "$(CARGO_BIN)" || (echo "error: Rust toolchain not found (CARGO=$(CARGO_BIN))." >&2; \
	  echo "  install from https://rustup.rs," >&2; \
	  echo "  or point at it explicitly:  make CARGO=\$$HOME/.cargo/bin/cargo build" >&2; \
	  echo "  note: under sudo the build automatically runs as \$$SUDO_USER," >&2; \
	  echo "  so that user's rustup toolchain is used (no root toolchain needed)." >&2; \
	  exit 1)
	@echo "cargo: $(CARGO_BIN)"

cli: check-go
	$(GO_BIN) build -o $(CLI_BIN) ./cmd/ebuild

daemon: check-cargo
ifdef _RUN_AS_USER
	$(_RUN_AS_USER) '$(_CARGO_ENV) $(CARGO_BIN) build $(CARGO_FLAGS)'
else
	$(CARGO_BIN) build $(CARGO_FLAGS)
endif
	@test -f $(TARGET_DIR)/$(PROFILE_DIR)/ebuild-daemon || (echo "error: cargo build produced no binary at $(TARGET_DIR)/$(PROFILE_DIR)/ebuild-daemon." >&2; \
	  echo "  scroll up for the compiler error; a common cause is an unset" >&2; \
	  echo "  CARGO_TARGET_DIR mismatch — set it explicitly, e.g.:" >&2; \
	  echo "    CARGO_TARGET_DIR=/tmp/ebuild-target sudo -E make install" >&2; \
	  exit 1)
	cp $(TARGET_DIR)/$(PROFILE_DIR)/ebuild-daemon $(DAEMON_OUT)

install: build
	install -d $(BINDIR)
	install -m 0755 $(CLI_BIN) $(BINDIR)/ebuild
	install -m 0755 $(DAEMON_OUT) $(BINDIR)/ebuild-daemon
	@echo "installed: $(BINDIR)/ebuild $(BINDIR)/ebuild-daemon"

uninstall:
	rm -f $(BINDIR)/ebuild $(BINDIR)/ebuild-daemon

test: test-go test-rust test-lua

test-go: check-go
	$(GO_BIN) vet ./...
	$(GO_BIN) test ./...

test-rust: check-cargo
ifdef _RUN_AS_USER
	$(_RUN_AS_USER) '$(_CARGO_ENV) $(CARGO_BIN) test $(CARGO_FLAGS)'
else
	$(CARGO_BIN) test $(CARGO_FLAGS)
endif

test-lua:
	for f in rules/*.lua; do $(LUAC) -p "$$f" || exit 1; done

clean:
	rm -f $(CLI_BIN) $(DAEMON_OUT)
	@if test -x "$(CARGO_BIN)"; then $(CARGO_BIN) clean --manifest-path internal/daemon/Cargo.toml; \
	else echo "clean: cargo not found, skipping cargo clean"; fi

help:
	@echo "targets: all cli daemon install uninstall test test-go test-rust test-lua clean"
	@echo "vars: PREFIX=$(PREFIX) DESTDIR=$(DESTDIR) CARGO_PROFILE=$(CARGO_PROFILE)"
