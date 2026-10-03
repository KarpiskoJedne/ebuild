// Zero-config toolchain detection, SHA-256 provenance verification,
// and hermetic environment construction for build actions.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// extLang maps file extensions to language IDs (mirrors worker.c).
var extLang = map[string]string{
	".c": "c", ".h": "c", ".cc": "cxx", ".cpp": "cxx", ".cxx": "cxx",
	".hpp": "cxx", ".rs": "rust", ".go": "go", ".java": "java",
	".kt": "kotlin", ".cs": "cs", ".swift": "swift", ".js": "js",
	".mjs": "js", ".ts": "ts", ".tsx": "ts", ".py": "python",
	".php": "php", ".pl": "perl", ".r": "r", ".lua": "lua",
	".f": "fortran", ".f90": "fortran", ".ada": "ada", ".m": "matlab",
}

// DetectLanguage returns the language ID for a path, or "unknown".
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if l, ok := extLang[ext]; ok {
		return l
	}
	if filepath.Ext(path) == ".R" {
		return "r"
	}
	return "unknown"
}

// DetectWorkspace scans root (skipping VCS dirs) and counts languages.
func DetectWorkspace(root string) (map[string]int, error) {
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, don't fail the scan
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".hg", ".svn", "node_modules", "target", ".ebuild", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if l := DetectLanguage(p); l != "unknown" {
			counts[l]++
		}
		return nil
	})
	return counts, err
}

// Toolchain is a pinned, verified compiler/runtime installation.
type Toolchain struct {
	Name    string // e.g. "gcc", "rustc", "go", "node"
	Version string // pinned version from ebuild.yml / .tool-versions
	Path    string // absolute binary path
	SHA256  string // hex digest of the binary at verification time
}

// Verify hashes the binary and compares against want (hex). It returns the
// computed digest so first-use flows can record provenance.
func (t *Toolchain) Verify(want string) (string, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	t.SHA256 = got
	if want != "" && !strings.EqualFold(got, want) {
		return got, fmt.Errorf("ebuild: toolchain %s sha256 mismatch: got %s want %s", t.Name, got, want)
	}
	return got, nil
}

// LookPath resolves a toolchain binary via PATH and records its version.
// Most tools answer `--version`; Go only answers `version`, so on failure
// the bare `version` subcommand is tried as a fallback.
func LookPath(name string) (*Toolchain, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("ebuild: toolchain %q not on PATH", name)
	}
	abs, _ := filepath.Abs(p)
	out, err := exec.Command(p, "--version").CombinedOutput()
	if err != nil {
		out, err = exec.Command(p, "version").CombinedOutput()
		if err != nil {
			return &Toolchain{Name: name, Path: abs}, nil
		}
	}
	return &Toolchain{Name: name, Version: firstLine(string(out)), Path: abs}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// HermeticEnv builds a sanitized environment for an action: a minimal
// allowlist (PATH/HOME/TMPDIR/locale) plus declared extras. Everything else
// from the host is stripped for reproducibility.
func HermeticEnv(extra []string, pathDirs []string) []string {
	allow := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true,
		"TZ": true, "LANG": true, "LC_ALL": true, "SYSTEMROOT": true}
	env := []string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && allow[kv[:i]] {
			env = append(env, kv)
		}
	}
	if len(pathDirs) > 0 {
		env = append(env, "PATH="+strings.Join(pathDirs, string(os.PathListSeparator)))
	}
	// Declared extras win over inherited values.
	for _, kv := range extra {
		if i := strings.IndexByte(kv, '='); i > 0 {
			k := kv[:i]
			out := env[:0]
			for _, e := range env {
				if ei := strings.IndexByte(e, '='); ei < 0 || e[:ei] != k {
					out = append(out, e)
				}
			}
			env = append(out, kv)
		}
	}
	return env
}

// CacheDir returns the per-user ebuild toolchain cache directory.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "ebuild", "toolchains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
