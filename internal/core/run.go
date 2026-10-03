// Custom user commands: lookup, shell-like splitting, and hermetic execution
// with inherited stdio so `ebuild run <name>` behaves like a local script.
package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindCommand returns the named custom command or an error listing known names.
func FindCommand(cfg *Config, name string) (*CommandDef, error) {
	for i := range cfg.Commands {
		if cfg.Commands[i].Name == name {
			return &cfg.Commands[i], nil
		}
	}
	// Targets are not commands: `ebuild runc //app:svc` is almost always a
	// `build`/`run` typo — say so instead of printing "unknown command".
	if IsTargetRef(name) {
		return nil, fmt.Errorf("ebuild: %q looks like a build target, not a command — use `ebuild build %s` or `ebuild run %s` (custom commands live in the `commands:` section of ebuild.yml)", name, name, name)
	}
	var names []string
	for _, c := range cfg.Commands {
		names = append(names, c.Name)
	}
	for _, b := range BuiltinCommands() {
		names = append(names, b.Name+"*")
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("ebuild: no custom commands defined (add a `commands:` section to ebuild.yml)")
	}
	return nil, fmt.Errorf("ebuild: unknown command %q (known: %s; * = built-in, always available)", name, strings.Join(names, ", "))
}

// IsTargetRef reports whether s looks like a build target reference
// (`//pkg:name`, `:name`, or anything containing a colon).
func IsTargetRef(s string) bool {
	return strings.HasPrefix(s, "//") || strings.HasPrefix(s, ":") || strings.Contains(s, ":")
}

// ExitError carries the process exit code across the CLI boundary.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

// SplitCommand splits a command line POSIX-style: whitespace separated with
// single-quote, double-quote, and backslash-escape support.
func SplitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote rune // 0, '\'' or '"'
	escaped := false
	flush := func() {
		// A quoted empty string still yields an argument.
		out = append(out, cur.String())
		cur.Reset()
	}
	had := false // current word has content (incl. empty quotes)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
			had = true
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0 && r == quote:
			quote = 0
			had = true
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			had = true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if had {
				flush()
				had = false
			}
		default:
			cur.WriteRune(r)
			had = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("ebuild: unfinished escape in %q", s)
	}
	if quote != 0 {
		return nil, fmt.Errorf("ebuild: unfinished quote in %q", s)
	}
	if had {
		flush()
	}
	return out, nil
}

// RunCommand executes a custom command: argv = split(cmd) + extra, running in
// workdir (relative to root) with a hermetic env + the command's own extras.
// Stdout/stderr stream live; a non-zero exit becomes *ExitError.
func RunCommand(ctx context.Context, root string, cmd CommandDef, extra []string, stdout, stderr io.Writer, stdin io.Reader) error {
	if strings.TrimSpace(cmd.Cmd) == "" {
		return fmt.Errorf("ebuild: command %q has empty cmd", cmd.Name)
	}
	argv, err := SplitCommand(cmd.Cmd)
	if err != nil {
		return err
	}
	argv = append(argv, extra...)
	dir := root
	if cmd.WorkDir != "" {
		dir = cmd.WorkDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("ebuild: command %q: %q not on PATH", cmd.Name, argv[0])
	}
	c := exec.CommandContext(ctx, bin, argv[1:]...)
	c.Dir = dir
	c.Env = HermeticEnv(cmd.Env, nil)
	c.Stdout = stdout
	c.Stderr = stderr
	c.Stdin = stdin
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: ee.ExitCode(), Err: fmt.Errorf("ebuild: command %q failed with exit %d", cmd.Name, ee.ExitCode())}
		}
		return fmt.Errorf("ebuild: command %q: %w", cmd.Name, err)
	}
	return nil
}

// RunCommandOS is RunCommand wired to the process stdio.
func RunCommandOS(ctx context.Context, root string, cmd CommandDef, extra []string) error {
	return RunCommand(ctx, root, cmd, extra, os.Stdout, os.Stderr, os.Stdin)
}

// ---------- built-in commands (always available, no config needed) ----------

// BuiltinCommands describes the built-ins for listings and help.
func BuiltinCommands() []CommandDef {
	return []CommandDef{
		{Name: "copy", Cmd: "copy <src...> <dst> — copy files/dirs inside the workspace"},
		{Name: "clean", Cmd: "clean [paths...] — remove .ebuild/ outputs (plus optional paths)"},
		{Name: "delete", Cmd: "delete <paths...> — remove files/dirs inside the workspace"},
	}
}

// IsBuiltin reports whether name is a built-in command.
func IsBuiltin(name string) bool {
	for _, b := range BuiltinCommands() {
		if b.Name == name {
			return true
		}
	}
	return false
}

// resolveInRoot joins p onto root and refuses escapes (absolute paths
// outside root, `..` traversal, and the root itself).
func resolveInRoot(root, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("ebuild: empty path")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ebuild: path %q escapes the workspace root", p)
	}
	return abs, nil
}

func copyOne(src, dst string, out io.Writer) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		// Recursive directory copy (target becomes dst/<base> like `cp -r`).
		base := filepath.Join(dst, filepath.Base(src))
		return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			t := filepath.Join(base, rel)
			if d.IsDir() {
				return os.MkdirAll(t, 0o755)
			}
			return copyFile(p, t)
		})
	}
	t := dst
	if di, err := os.Stat(dst); err == nil && di.IsDir() {
		t = filepath.Join(dst, filepath.Base(src))
	}
	if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
		return err
	}
	if err := copyFile(src, t); err != nil {
		return err
	}
	fmt.Fprintf(out, "copied %s -> %s\n", src, t)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

// RunBuiltin executes a built-in command. It returns (true, err) when name
// was a built-in (err may still be an execution failure), (false, nil) when
// name is not a built-in at all.
func RunBuiltin(root, name string, args []string, stdout io.Writer) (bool, error) {
	switch name {
	case "copy":
		if len(args) < 2 {
			return true, fmt.Errorf("usage: ebuild run copy <src...> <dst>")
		}
		dst, err := resolveInRoot(root, args[len(args)-1])
		if err != nil {
			return true, err
		}
		for _, s := range args[:len(args)-1] {
			src, err := resolveInRoot(root, s)
			if err != nil {
				return true, err
			}
			if err := copyOne(src, dst, stdout); err != nil {
				return true, fmt.Errorf("ebuild: copy %s: %w", s, err)
			}
		}
		return true, nil
	case "clean":
		targets := []string{filepath.Join(root, ".ebuild")}
		for _, a := range args {
			p, err := resolveInRoot(root, a)
			if err != nil {
				return true, err
			}
			targets = append(targets, p)
		}
		for _, t := range targets {
			if err := os.RemoveAll(t); err != nil {
				return true, fmt.Errorf("ebuild: clean %s: %w", t, err)
			}
			fmt.Fprintf(stdout, "removed %s\n", t)
		}
		return true, nil
	case "delete":
		if len(args) == 0 {
			return true, fmt.Errorf("usage: ebuild run delete <paths...>")
		}
		for _, a := range args {
			p, err := resolveInRoot(root, a)
			if err != nil {
				return true, err
			}
			if err := os.RemoveAll(p); err != nil {
				return true, fmt.Errorf("ebuild: delete %s: %w", a, err)
			}
			fmt.Fprintf(stdout, "deleted %s\n", p)
		}
		return true, nil
	}
	return false, nil
}
