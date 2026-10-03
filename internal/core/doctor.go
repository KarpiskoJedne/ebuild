// ebuild doctor: environment health checks (toolchains, daemon, workspace,
// disk). Pure stdlib; fatal vs advisory is encoded in Status.
package core

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	daemon "github.com/ebuild/ebuild/internal/daemon"
)

// Check statuses.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
)

// Check is one doctor result row.
type Check struct {
	Name   string
	Status string
	Detail string
}

// Doctor inspects the environment for root ("" = cwd) and returns one row
// per probe. A missing workspace or unparsable config is a failure;
// missing optional toolchains or an unreachable daemon are warnings.
func Doctor(root string) []Check {
	var out []Check
	add := func(name, status, detail string) {
		out = append(out, Check{Name: name, Status: status, Detail: detail})
	}
	if root == "" {
		if cwd, err := filepath.Abs("."); err == nil {
			root = cwd
		}
	}
	// Required-feel toolchains (warn: ebuild can provision via asdf).
	for _, bin := range []string{"go", "cc", "cargo", "python3", "node", "luac"} {
		p, err := exec.LookPath(bin)
		if err != nil {
			add("toolchain:"+bin, CheckWarn, "not on PATH (asdf can provision)")
			continue
		}
		ver := ""
		if out, err := exec.Command(p, "--version").CombinedOutput(); err == nil {
			ver = firstLine(string(out))
			if len(ver) > 60 {
				ver = ver[:60]
			}
		}
		add("toolchain:"+bin, CheckOK, ver)
	}
	// Daemon reachability (advisory: builds work without it, watching doesn't).
	sock := daemon.DefaultSocketPath()
	if c, err := daemon.Dial(sock); err != nil {
		add("daemon", CheckWarn, fmt.Sprintf("unreachable at %s (%v)", sock, shortErr(err)))
	} else {
		st, err := c.Status()
		_ = c.Close()
		if err != nil {
			add("daemon", CheckWarn, fmt.Sprintf("connected but status failed: %v", shortErr(err)))
		} else {
			add("daemon", CheckOK, st)
		}
	}
	// Workspace + config.
	ws, err := DiscoverWorkspace(root)
	if err != nil {
		add("workspace", CheckFail, "no ebuild.yml found above "+root)
		return out
	}
	add("workspace", CheckOK, ws)
	cfg, err := ParseFile(filepath.Join(ws, "ebuild.yml"))
	if err != nil {
		add("config", CheckFail, err.Error())
		return out
	}
	add("config", CheckOK, fmt.Sprintf("%d targets, %d commands, %d toolchains",
		len(cfg.Targets), len(cfg.Commands), len(cfg.Toolchains)))
	// Disk space for CAS + outputs.
	if free, err := diskFree(ws); err != nil {
		add("disk", CheckWarn, "free space unknown: "+shortErr(err))
	} else if free < 100<<20 {
		add("disk", CheckFail, fmt.Sprintf("only %s free under %s", humanBytes(free), ws))
	} else if free < 1<<30 {
		add("disk", CheckWarn, fmt.Sprintf("only %s free under %s", humanBytes(free), ws))
	} else {
		add("disk", CheckOK, humanBytes(free)+" free")
	}
	return out
}

// HasFailures reports whether any check failed.
func HasFailures(checks []Check) bool {
	for _, c := range checks {
		if c.Status == CheckFail {
			return true
		}
	}
	return false
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func humanBytes(b int64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%dB", b)
	}
	v, e := float64(b), 0
	for v >= u && e < 4 {
		v /= u
		e++
	}
	return fmt.Sprintf("%.1f%cB", v, "KMGT"[e-1])
}
