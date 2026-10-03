// ebuild CLI: build, clean, daemon, query, migrate. CGO_ENABLED=0.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ebuild/ebuild/internal/core"
	daemon "github.com/ebuild/ebuild/internal/daemon"
)

const cliVersion = "0.1.0"

const usage = `ebuild — Easy Build

Usage:
  ebuild build [//target...|all] [--root DIR] [--jobs N]
  ebuild run [//target|all] [--root DIR] [-- args...]
  ebuild runc <command> [--root DIR] [-- args...]
  ebuild init [--root DIR] [--force]
  ebuild clean [--root DIR]
  ebuild daemon [--socket PATH] [--tui]
  ebuild query (deps|rdeps|critical|impact) TARGET... [--root DIR]
  ebuild migrate --from=bazel|cmake|make|python [--root DIR]
  ebuild version
  ebuild doctor [--root DIR]
  ebuild help [command]

Run 'ebuild help <command>' for details.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	// Global version flags work anywhere in first position.
	if os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "-version" {
		fmt.Printf("ebuild %s\n", cliVersion)
		return
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = runBuild(os.Args[2:])
	case "all":
		// Top-level alias: `ebuild all` == `ebuild build all`.
		err = runBuild(append(os.Args[2:], "all"))
	case "run":
		err = runRun(os.Args[2:])
	case "runc":
		err = runRunc(os.Args[2:])
	case "init":
		err = runInit(os.Args[2:])
	case "clean":
		err = runClean(os.Args[2:])
	case "daemon":
		err = runDaemon(os.Args[2:])
	case "query":
		err = runQuery(os.Args[2:])
	case "migrate":
		err = runMigrate(os.Args[2:])
	case "version":
		fmt.Printf("ebuild %s\n", cliVersion)
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "help":
		err = runHelp(os.Args[2:])
	case "-h", "--help":
		fmt.Print(usage)
	default:
		err = runHelp(os.Args[1:])
	}
	if err != nil {
		var exit *core.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintf(os.Stderr, "ebuild: error: %v\n", exit.Err)
			os.Exit(exit.Code)
		}
		fmt.Fprintf(os.Stderr, "ebuild: error: %v\n", err)
		os.Exit(1)
	}
}

func workspaceRoot(rootFlag string) (string, error) {
	if rootFlag != "" {
		return filepath.Abs(rootFlag)
	}
	cwd, _ := os.Getwd()
	return core.DiscoverWorkspace(cwd)
}

func loadConfig(root string) (*core.Config, error) {
	cfg, err := core.ParseFile(filepath.Join(root, "ebuild.yml"))
	if err != nil {
		return nil, err
	}
	if cfg.Root == "" || cfg.Root == "." {
		cfg.Root = root
	}
	return cfg, nil
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "workspace root")
	jobs := fs.Int("jobs", 0, "max workers (0 = NumCPU)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := workspaceRoot(*rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	plan, err := cfg.ToPlan(fs.Args())
	if err != nil {
		return err
	}
	return executePlan(root, plan.Actions, *jobs)
}

// executePlan runs actions in dependency waves and prints one row per
// result (ok/FAIL with captured output). Skipped dependents of failed
// actions are reported, not silently dropped.
func executePlan(root string, actions []core.Action, jobs int) error {
	results, err := core.RunPlan(root, actions, jobs)
	for _, r := range results {
		if r.Err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", r.Target, r.Err)
			if out := strings.TrimSpace(r.Output); out != "" {
				fmt.Fprintf(os.Stderr, "%s\n", out)
			}
		} else {
			fmt.Printf("ok %s (%s)\n", r.Target, r.Duration.Truncate(time.Millisecond))
		}
	}
	return err
}

func targetNames(actions []core.Action) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.Target
	}
	return out
}

// splitRootFlag extracts --root/--root=X anywhere in args (the flag
// package stops parsing at the first positional, so `run greet --root .`
// must still work). A `--` terminator passes the remainder through, so
// command args starting with dashes survive.
func splitRootFlag(args []string) (string, []string) {
	var rootFlag string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--root" && i+1 < len(args) {
			rootFlag = args[i+1]
			i++
			continue
		}
		if r, ok := strings.CutPrefix(a, "--root="); ok {
			rootFlag = r
			continue
		}
		rest = append(rest, a)
	}
	return rootFlag, rest
}

// stripDashDash drops a leading "--" argument separator (run/runc calling
// convention: `ebuild run //t -- args...`).
func stripDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

// runRun builds a binary target and executes it (bazel-style `run`).
// Custom commands live under `runc`; a non-target name here hints at it.
func runRun(args []string) error {
	rootFlag, rest := splitRootFlag(args)
	if len(rest) == 0 {
		return fmt.Errorf("usage: ebuild run //target [--root DIR] [-- args...]  (custom commands: ebuild runc)")
	}
	root, err := workspaceRoot(rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	name, extra := rest[0], stripDashDash(rest[1:])
	if name == "all" {
		if len(extra) > 0 {
			return fmt.Errorf("ebuild: cannot pass args to `run all` (run one target instead)")
		}
		return runAll(root, cfg)
	}
	if !core.IsTargetRef(name) {
		// Custom command passed to run: point at runc (or built-ins).
		if core.IsBuiltin(name) {
			if ok, berr := core.RunBuiltin(root, name, extra, os.Stdout); ok {
				return berr
			}
		}
		if _, cerr := core.FindCommand(cfg, name); cerr == nil {
			return fmt.Errorf("ebuild: %q is a custom command — use `ebuild runc %s`", name, name)
		}
		return fmt.Errorf("ebuild: unknown target %q (custom commands: ebuild runc)", name)
	}
	t, err := core.FindTarget(cfg, name)
	if err != nil {
		return err
	}
	if !core.RunnableRule(t.Rule) {
		return fmt.Errorf("ebuild: target %s (rule %s) produces no executable — runnable rules: cc_binary, cxx_binary, go_binary, rust_binary, swift_binary, zig_binary, nim_binary, crystal_app, ocaml_executable", name, t.Rule)
	}
	plan, err := cfg.ToPlan([]string{name})
	if err != nil {
		return err
	}
	if err := executePlan(root, plan.Actions, 0); err != nil {
		return fmt.Errorf("ebuild: not running %s: %w", name, err)
	}
	return execBinary(root, name, *t, extra)
}

// runAll builds everything, then executes each runnable binary target in
// dependency order, stopping at the first failure.
func runAll(root string, cfg *core.Config) error {
	plan, err := cfg.ToPlan(nil)
	if err != nil {
		return err
	}
	if err := executePlan(root, plan.Actions, 0); err != nil {
		return fmt.Errorf("ebuild: not running all: %w", err)
	}
	ran := 0
	for _, t := range core.TopoSort(cfg.Targets) {
		if !core.RunnableRule(t.Rule) {
			continue
		}
		fmt.Printf("== run %s ==\n", t.Name)
		if err := execBinary(root, t.Name, t, nil); err != nil {
			return err
		}
		ran++
	}
	if ran == 0 {
		return fmt.Errorf("ebuild: no runnable binary targets (runnable rules: cc_binary, cxx_binary, go_binary, rust_binary, swift_binary, zig_binary, nim_binary, crystal_app, ocaml_executable)")
	}
	return nil
}

// execBinary launches a built target output with extra args on inherited
// stdio, propagating the exit code.
func execBinary(root, name string, t core.Target, extra []string) error {
	bin := core.TargetOutput(t)
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(root, bin)
	}
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("ebuild: build of %s produced no binary at %s", name, bin)
	}
	cmd := exec.Command(bin, extra...)
	cmd.Dir = root
	cmd.Env = core.HermeticEnv(nil, nil)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return &core.ExitError{Code: ee.ExitCode(), Err: fmt.Errorf("ebuild: %s exited with %d", name, ee.ExitCode())}
		}
		return fmt.Errorf("ebuild: run %s: %w", name, err)
	}
	return nil
}

func runRunc(args []string) error {
	rootFlag, rest := splitRootFlag(args)
	if len(rest) == 0 {
		// Bare `ebuild run`: list commands, offer interactive pick on a TTY.
		root, err := workspaceRoot(rootFlag)
		if err != nil {
			return err
		}
		cfg, err := loadConfig(root)
		if err != nil {
			return err
		}
		return pickAndRun(root, cfg)
	}
	root, err := workspaceRoot(rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	cmd, err := core.FindCommand(cfg, rest[0])
	if err != nil {
		// Built-ins run without any config entry.
		if core.IsBuiltin(rest[0]) {
			if ok, berr := core.RunBuiltin(root, rest[0], stripDashDash(rest[1:]), os.Stdout); ok {
				return berr
			}
		}
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return core.RunCommandOS(ctx, root, *cmd, stripDashDash(rest[1:]))
}

func runClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := workspaceRoot(*rootFlag)
	if err != nil {
		return err
	}
	stale := []string{filepath.Join(root, ".ebuild", "trace.jsonl")}
	removed := 0
	for _, p := range stale {
		if err := os.Remove(p); err == nil {
			removed++
		}
	}
	// Drop local CAS objects unreferenced in the last 24h via the GC engine.
	n, err := daemon.SweepOld(filepath.Join(root, ".ebuild", "cas"), 24*time.Hour)
	if err == nil {
		removed += n
	}
	fmt.Printf("clean: removed %d stale entries under %s\n", removed, root)
	return nil
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	sock := fs.String("socket", daemon.DefaultSocketPath(), "ipc socket path")
	tui := fs.Bool("tui", false, "show live dashboard")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Printf("ebuild daemon listening on %s\n", *sock)
	if *tui {
		m := core.NewModel(4)
		m.SetResources(core.SampleResources())
		fmt.Print(m.View())
	}
	// The production daemon binary is worker_host (Rust). This command probes
	// the socket so scripts can wait for readiness.
	c, err := daemon.Dial(*sock)
	if err != nil {
		fmt.Printf("daemon: no running daemon at %s (%v)\n", *sock, err)
		fmt.Println("daemon: start ./ebuild-daemon --socket " + *sock)
		return nil
	}
	defer c.Close()
	st, err := c.Status()
	if err != nil {
		return err
	}
	fmt.Printf("daemon: %s\n", st)
	return nil
}

func runQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: ebuild query (deps|rdeps|critical|impact) TARGET... ")
	}
	verb, targets := rest[0], rest[1:]
	root, err := workspaceRoot(*rootFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	g := core.NewGraph(cfg.Targets)
	for _, t := range targets {
		switch verb {
		case "deps":
			fmt.Println(strings.Join(g.TransitiveDeps(t), "\n"))
		case "rdeps":
			fmt.Println(strings.Join(g.ReverseDeps(t), "\n"))
		case "critical":
			fmt.Println(strings.Join(g.CriticalPath(t, nil), "\n"))
		case "impact":
			fmt.Println(strings.Join(g.Impact(cfg.Targets, []string{t}), "\n"))
		default:
			return fmt.Errorf("unknown query verb %q", verb)
		}
	}
	return nil
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := *rootFlag
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	checks := core.Doctor(root)
	width := 0
	for _, c := range checks {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	mark := map[string]string{"ok": "ok  ", "warn": "WARN", "fail": "FAIL"}
	for _, c := range checks {
		m, ok := mark[c.Status]
		if !ok {
			m = "????"
		}
		fmt.Printf("[%s] %-*s  %s\n", m, width, c.Name, c.Detail)
	}
	if core.HasFailures(checks) {
		return fmt.Errorf("ebuild: doctor found failures")
	}
	return nil
}

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	from := fs.String("from", "", "bazel|cmake|make|python")
	rootFlag := fs.String("root", "", "project root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("usage: ebuild migrate --from=bazel|cmake|make|python [--root DIR]")
	}
	root, err := workspaceRoot(*rootFlag)
	if err != nil {
		cwd, _ := os.Getwd()
		root = cwd
		if *rootFlag != "" {
			root = *rootFlag
		}
	}
	doc, err := core.Migrate(core.From(*from), root)
	if err != nil {
		return err
	}
	out := filepath.Join(root, "ebuild.yml")
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		return err
	}
	fmt.Printf("migrated %s → %s\n", *from, out)
	return nil
}

// helpTexts holds the long help per command, in stable display order.
var helpOrder = []string{"build", "run", "runc", "init", "clean", "daemon", "query", "migrate", "version", "doctor", "help"}

var helpTexts = map[string]string{
	"build": `ebuild build [//target...|all] [--root DIR] [--jobs N]

  Build targets from ebuild.yml (default, "all" and //... = everything).
  Examples:
    ebuild build //app:svc
    ebuild build all --jobs 8`,
	"run": `ebuild run [//target|all] [--root DIR] [-- args...]

  Build binary target(s) and execute them (like 'bazel run').
  "all" runs every runnable binary in dependency order, fail-fast.
  Runnable rules: cc_binary, cxx_binary, go_binary, rust_binary,
  swift_binary, zig_binary, nim_binary, crystal_app, ocaml_executable.
  Examples:
    ebuild run //app:svc
    ebuild run all
    ebuild run //app:svc -- --port 8080
  For custom commands use 'ebuild runc' instead.`,
	"runc": `ebuild runc <command> [--root DIR] [-- args...]

  Run a custom command from the commands: section of ebuild.yml, or a
  built-in (copy, clean, delete — always available, sandboxed to the
  workspace). Bare 'ebuild runc' lists everything and offers a pick.
  Examples:
    ebuild runc test -- ./svc/...
    ebuild runc copy app/a.txt backup/`,
	"init": `ebuild init [--root DIR] [--force]

  Write a minimal ebuild.yml skeleton (project name, empty targets and
  commands). Edit it by hand afterwards; --force overwrites.`,
	"clean": `ebuild clean [--root DIR]

  Remove stale trace data and unreferenced local CAS objects.`,
	"daemon": `ebuild daemon [--socket PATH] [--tui]

  Probe the native daemon socket (start it with ebuild-daemon --socket
  PATH); --tui shows the live dashboard model.`,
	"query": `ebuild query (deps|rdeps|critical|impact) TARGET... [--root DIR]

  Inspect the dependency graph: transitive deps, reverse deps, the
  critical path, or change-impact for a file list.`,
	"migrate": `ebuild migrate --from=bazel|cmake|make|python [--root DIR]

  Convert a legacy project layout into ebuild.yml.`,
	"version": `ebuild version   (also: ebuild --version, ebuild -v)

  Print the CLI version.`,
	"doctor": `ebuild doctor [--root DIR]

  Check the environment: toolchains, daemon reachability, workspace
  config validity, and free disk space. Exits non-zero on failures.`,
	"help": `ebuild help [command]

  Show this overview or details for one command.`,
}

func runHelp(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	name := args[0]
	if text, ok := helpTexts[name]; ok {
		fmt.Println(text)
		return nil
	}
	// Suggest closest command names (prefix or containment match).
	var sug []string
	for _, n := range helpOrder {
		if strings.HasPrefix(n, name) || strings.Contains(n, name) {
			sug = append(sug, n)
		}
	}
	if len(sug) > 0 {
		return fmt.Errorf("ebuild: unknown command %q (did you mean: %s?)\n%s", name, strings.Join(sug, ", "), usage)
	}
	return fmt.Errorf("ebuild: unknown command %q\n%s", name, usage)
}

func pickAndRun(root string, cfg *core.Config) error {
	if len(cfg.Commands) == 0 && len(core.BuiltinCommands()) == 0 {
		return fmt.Errorf("no custom commands defined (add a `commands:` section to ebuild.yml)")
	}
	fmt.Println("custom commands:")
	for i, c := range cfg.Commands {
		fmt.Printf("  [%d] %-16s %s\n", i+1, c.Name, c.Cmd)
	}
	fmt.Println("built-ins (always available):")
	for _, b := range core.BuiltinCommands() {
		fmt.Printf("  %-19s %s\n", b.Name, b.Cmd)
	}
	if !isTTY() {
		return fmt.Errorf("usage: ebuild run <command> [-- args...]")
	}
	fmt.Print("run which? [number/name, empty=cancel]: ")
	choice, _ := formReader().ReadString('\n')
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return nil
	}
	if core.IsBuiltin(choice) {
		fmt.Print("args (space-separated, optional): ")
		argLine, _ := formReader().ReadString('\n')
		var args []string
		if strings.TrimSpace(argLine) != "" {
			var err error
			args, err = core.SplitCommand(strings.TrimSpace(argLine))
			if err != nil {
				return err
			}
		}
		if ok, err := core.RunBuiltin(root, choice, args, os.Stdout); ok {
			return err
		}
	}
	var picked *core.CommandDef
	if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(cfg.Commands) {
		picked = &cfg.Commands[n-1]
	} else {
		picked, err = core.FindCommand(cfg, choice)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return core.RunCommandOS(ctx, root, *picked, nil)
}

// ---------- init form machinery (stdio only, works over any terminal) ----------

var stdinReader = bufio.NewReader(os.Stdin)

func formReader() *bufio.Reader { return stdinReader }

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "project root")
	forceFlag := fs.Bool("force", false, "overwrite an existing ebuild.yml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	root := cwd
	if *rootFlag != "" {
		var err error
		root, err = filepath.Abs(*rootFlag)
		if err != nil {
			return err
		}
	}
	out := filepath.Join(root, "ebuild.yml")
	if _, err := os.Stat(out); err == nil && !*forceFlag {
		return fmt.Errorf("ebuild.yml already exists (use --force to overwrite or edit it manually)")
	}
	doc := fmt.Sprintf(`version: 1

project:
  name: %s
  root: .

targets: []

commands: []
`, filepath.Base(root))
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s — add targets and commands, then run `ebuild build //...`\n", out)
	return nil
}
