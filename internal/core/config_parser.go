// Package core is the shared model of the ebuild Go orchestrator.
// All code here is stdlib-only and builds with CGO_ENABLED=0.
package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Target is a single buildable unit decoded from ebuild.yml.
type Target struct {
	Name       string
	Rule       string
	Srcs       []string
	Hdrs       []string
	Deps       []string
	Out        string
	Dockerfile string
	Context    string
	Tags       []string
	Languages  []string
	CFlags     []string
	// Toolchain selects the toolchain variant for the rule
	// (e.g. cc_binary: cc|gcc|clang|tcc|cl; python_package: uv|pip|poetry).
	// Empty means the rule default.
	Toolchain string
	// Cmd is the raw command for the custom_script rule.
	Cmd string
	// Entry is the entry file for bundling rules (typescript_app, tailwind).
	// Defaults to Srcs[0].
	Entry string
	// Exe is the dune executable target for ocaml_executable.
	Exe string
	// Generator is the codegen backend (openapi_codegen, e.g. "go").
	Generator string
	// Main is the main class for executable jars (java_binary).
	Main string
	// Dir is the working subdirectory for project-based rules
	// (terraform_apply, elixir_mix, haskell_package, ...). Default ".".
	Dir string
}

// CommandDef is a user-defined command from the `commands:` section.
// Two styles are accepted:
//
//	commands:
//	  - name: test            # list style (supports cmd/workdir/env)
//	    cmd: go test ./...
//	    workdir: svc
//	    env: ["GOFLAGS=-count=1"]
//	  lint: golangci-lint run  # shorthand style: name -> cmd
type CommandDef struct {
	Name    string
	Cmd     string
	WorkDir string
	Env     []string
}

// Config is a parsed ebuild.yml workspace.
type Config struct {
	Version    int
	Project    string
	Root       string
	Toolchains map[string]string
	Targets    []Target
	Commands   []CommandDef
}

// Action is one executable build step derived from a Target.
type Action struct {
	Name    string // owning target name (//pkg:name), for selection
	Rule    string
	Target  string // output path (or target name for daemon-expanded rules)
	WorkDir string
	Argv    string
	// Needs lists output paths that must be built before this action
	// (object files for archives, libraries for links). The scheduler
	// executes the plan in waves honoring these edges.
	Needs []string
}

// Plan is an ordered list of actions (topologically sorted by deps).
type Plan struct {
	Actions []Action
}

// DiscoverWorkspace walks up from start looking for ebuild.yml.
func DiscoverWorkspace(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(abs, "ebuild.yml")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("ebuild: no ebuild.yml found above %s", start)
		}
		abs = parent
	}
}

// ParseFile reads ebuild.yml (indentation-aware subset parser for the
// schema documented in ebuild.yml: version/project/toolchains/targets).
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg := &Config{Toolchains: map[string]string{}}
	var cur *Target
	var curCmd *CommandDef
	var section string // "", "project", "toolchains", "targets", "commands"
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0 && strings.HasSuffix(trimmed, ":"):
			section = strings.TrimSuffix(trimmed, ":")
			cur = nil
			curCmd = nil
		case section == "" && strings.HasPrefix(trimmed, "version:"):
			v, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "version:")))
			cfg.Version = v
		case section == "project" && strings.HasPrefix(trimmed, "name:"):
			cfg.Project = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")))
		case section == "project" && strings.HasPrefix(trimmed, "root:"):
			cfg.Root = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "root:")))
		case section == "toolchains" && strings.Contains(trimmed, ":"):
			k, v := splitKV(trimmed)
			cfg.Toolchains[k] = unquote(v)
		case section == "targets" && strings.HasPrefix(trimmed, "- name:"):
			t := Target{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			cfg.Targets = append(cfg.Targets, t)
			cur = &cfg.Targets[len(cfg.Targets)-1]
			curCmd = nil
		case section == "commands" && strings.HasPrefix(trimmed, "- name:"):
			c := CommandDef{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			cfg.Commands = append(cfg.Commands, c)
			curCmd = &cfg.Commands[len(cfg.Commands)-1]
			cur = nil
		case section == "commands" && indent == 2 && strings.Contains(trimmed, ":"):
			// Shorthand style: `lint: golangci-lint run`.
			k, v := splitKV(trimmed)
			if k != "" && v != "" && !strings.HasPrefix(v, "[") {
				cfg.Commands = append(cfg.Commands, CommandDef{Name: k, Cmd: unquote(v)})
			}
			cur = nil
			curCmd = nil
		case curCmd != nil && indent >= 4 && strings.Contains(trimmed, ":"):
			k, v := splitKV(trimmed)
			switch k {
			case "cmd":
				curCmd.Cmd = unquote(v)
			case "workdir":
				curCmd.WorkDir = unquote(v)
			case "env":
				curCmd.Env = parseStringList(v)
			}
		case cur != nil && indent >= 4 && strings.Contains(trimmed, ":"):
			k, v := splitKV(trimmed)
			assignTargetField(cur, k, parseStringList(v))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		return nil, fmt.Errorf("ebuild: %s: missing or zero version", path)
	}
	return cfg, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func splitKV(s string) (string, string) {
	i := strings.Index(s, ":")
	if i < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
}

// parseStringList handles: ["a", "b"], single scalar, or "".
func parseStringList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(inner, ",") {
			if v := unquote(strings.TrimSpace(part)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return []string{unquote(s)}
}

func assignTargetField(t *Target, key string, vals []string) {
	first := ""
	if len(vals) > 0 {
		first = vals[0]
	}
	switch key {
	case "rule":
		t.Rule = first
	case "srcs":
		t.Srcs = vals
	case "hdrs":
		t.Hdrs = vals
	case "deps":
		t.Deps = vals
	case "out":
		t.Out = first
	case "dockerfile":
		t.Dockerfile = first
	case "context":
		t.Context = first
	case "tags":
		t.Tags = vals
	case "languages":
		t.Languages = vals
	case "cflags":
		t.CFlags = vals
	case "toolchain":
		t.Toolchain = first
	case "cmd":
		t.Cmd = first
	case "entry":
		t.Entry = first
	case "exe":
		t.Exe = first
	case "generator":
		t.Generator = first
	case "main":
		t.Main = first
	case "dir":
		t.Dir = first
	}
}

// runnableRules are target rules that produce a directly executable file.
var runnableRules = map[string]bool{
	"cc_binary": true, "cxx_binary": true, "go_binary": true,
	"rust_binary": true, "swift_binary": true, "zig_binary": true,
	"nim_binary": true, "crystal_app": true, "ocaml_executable": true,
}

// RunnableRule reports whether rule produces an executable `ebuild run` can launch.
func RunnableRule(rule string) bool { return runnableRules[rule] }

// FindTarget returns the named target or an error listing known names.
// Shorthand refs (//pkg, :name) are normalized first.
func FindTarget(cfg *Config, name string) (*Target, error) {
	name = NormalizeTargetRef(name)
	for i := range cfg.Targets {
		if cfg.Targets[i].Name == name {
			return &cfg.Targets[i], nil
		}
	}
	var names []string
	for _, t := range cfg.Targets {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("ebuild: no targets defined in ebuild.yml")
	}
	return nil, fmt.Errorf("ebuild: no such target %q (known: %s)", name, strings.Join(names, ", "))
}

// shortName strips the //pkg: prefix (//app:svc -> svc).
func shortName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return strings.ReplaceAll(name[i+1:], "/", "_")
	}
	return strings.ReplaceAll(name, "/", "_")
}

// TargetOutput returns the output artifact path for a target: the explicit
// `out` field or the recipe default. Marker outputs (.ok stamps) are real
// files the recipes touch, so output verification and Needs edges work.
func TargetOutput(t Target) string {
	if t.Out != "" {
		return t.Out
	}
	short := shortName(t.Name)
	switch t.Rule {
	case "cc_binary", "cxx_binary", "go_binary", "swift_binary", "zig_binary", "nim_binary", "crystal_app":
		return ".ebuild/bin/" + short
	case "ocaml_executable":
		exe := t.Exe
		if exe == "" {
			exe = short
		}
		return "_build/default/" + exe
	case "rust_binary":
		return "target/debug/" + short
	case "rust_library":
		return "target/debug/lib" + short + ".rlib"
	case "cc_library", "cxx_library", "swift_library":
		return ".ebuild/lib/lib" + short + ".a"
	case "go_library":
		return ".ebuild/lib/lib" + short + ".a"
	case "java_binary", "java_library", "kotlin_app", "kotlin_library", "scala_app", "scala_library":
		return "dist/" + short + ".jar"
	case "typescript_app":
		return "dist/" + short + ".js"
	case "typescript_library":
		return ".ebuild/tsc/" + short + ".ok"
	case "python_package":
		return ".ebuild/python/" + short + ".ok"
	case "python_test":
		return ".ebuild/pytest/" + short + ".ok"
	case "protobuf_library":
		return ".ebuild/proto/" + short + "/.gen-ok"
	case "grpc_service":
		return ".ebuild/grpc/" + short + "/.gen-ok"
	case "docker_image":
		return ".ebuild/docker/" + short + ".iid"
	case "helm_chart":
		return ".ebuild/helm/" + short + ".ok"
	case "terraform_apply":
		return ".ebuild/terraform/" + short + ".ok"
	case "ansible_playbook", "ansible_lint":
		return ".ebuild/ansible/" + short + ".ok"
	case "shell_script":
		return ".ebuild/sh/" + short + ".ok"
	case "ruby_gem":
		return ".ebuild/ruby/" + short + ".ok"
	case "php_composer":
		return ".ebuild/php/" + short + ".ok"
	case "csharp_assembly", "fsharp_assembly":
		return ".ebuild/dotnet/" + short + ".ok"
	case "clojure_project":
		return ".ebuild/clojure/" + short + ".ok"
	case "haskell_package":
		return ".ebuild/haskell/" + short + ".ok"
	case "elixir_mix":
		return ".ebuild/mix/" + short + ".ok"
	case "erlang_otp":
		return ".ebuild/otp/" + short + ".ok"
	case "julia_script":
		return ".ebuild/julia/" + short + ".ok"
	case "r_package":
		return ".ebuild/R/" + short + ".ok"
	case "latex_document":
		main := t.Entry
		if main == "" {
			for _, s := range t.Srcs {
				if strings.HasSuffix(s, ".tex") {
					main = s
					break
				}
			}
		}
		base := short
		if main != "" {
			base = strings.TrimSuffix(filepath.Base(main), ".tex")
		}
		return "dist/" + base + ".pdf"
	case "openapi_codegen":
		return ".ebuild/openapi/" + short + "/.openapi-ok"
	case "graphql_schema":
		return ".ebuild/graphql/" + short + ".ok"
	case "tailwind_css":
		return "dist/" + short + ".css"
	case "webpack_bundle":
		return ".ebuild/webpack/" + short + ".ok"
	case "vite_build":
		return ".ebuild/vite/" + short + ".ok"
	case "security_scan":
		return ".ebuild/security/" + short + ".ok"
	default:
		return t.Name
	}
}

// ObjectPath maps a source file to its deterministic object location under
// .ebuild/obj, preserving the relative directory layout (no collisions).
func ObjectPath(src string) string {
	return filepath.Join(".ebuild/obj", strings.TrimSuffix(src, filepath.Ext(src))+".o")
}

// libraryRules marks rules whose outputs are linkable libraries.
var libraryRules = map[string]bool{
	"cc_library": true, "cxx_library": true, "swift_library": true,
	"go_library": true, "rust_library": true,
	"java_library": true, "kotlin_library": true, "scala_library": true,
}

// SelectActions filters plan actions by target wants. "all" and "//..."
// select everything; otherwise names (shorthands allowed) select their
// closure including transitive deps.
func SelectActions(cfg *Config, plan *Plan, wants []string) ([]Action, error) {
	for _, w := range wants {
		if w == "all" || w == "//..." {
			return plan.Actions, nil
		}
	}
	if len(wants) == 0 {
		return plan.Actions, nil
	}
	g := NewGraph(cfg.Targets)
	keep := map[string]bool{}
	for _, w := range wants {
		w = NormalizeTargetRef(w)
		keep[w] = true
		for _, d := range g.TransitiveDeps(w) {
			keep[d] = true
		}
	}
	var actions []Action
	for _, a := range plan.Actions {
		if keep[a.Name] {
			actions = append(actions, a)
		}
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("ebuild: no such targets: %s", strings.Join(wants, ", "))
	}
	return actions, nil
}

// ToPlan expands wants (target names, shorthands, "all"/"//..." or empty
// for everything) in topological order. Only the selected closure is
// expanded, so missing toolchains of unrelated targets never fail a build.
// Library outputs are precomputed so binaries can link against them.
func (c *Config) ToPlan(wants []string) (*Plan, error) {
	all := len(wants) == 0
	keep := map[string]bool{}
	if !all {
		for _, w := range wants {
			if w == "all" || w == "//..." {
				all = true
				break
			}
		}
	}
	order := TopoSort(c.Targets)
	if !all {
		g := NewGraph(c.Targets)
		for _, w := range wants {
			w = NormalizeTargetRef(w)
			keep[w] = true
			for _, d := range g.TransitiveDeps(w) {
				keep[d] = true
			}
		}
		var sel []Target
		for _, t := range order {
			if keep[t.Name] {
				sel = append(sel, t)
			}
		}
		if len(sel) == 0 {
			return nil, fmt.Errorf("ebuild: no such targets: %s", strings.Join(wants, ", "))
		}
		order = sel
	}
	plan := &Plan{}
	workdir := c.Root
	if workdir == "" {
		workdir = "."
	}
	libs := map[string]string{}
	hdrs := map[string][]string{}
	for _, t := range order {
		if libraryRules[t.Rule] {
			libs[t.Name] = TargetOutput(t)
			hdrs[t.Name] = append(append([]string{}, t.Hdrs...), t.Srcs...)
		}
	}
	for _, t := range order {
		actions, err := ruleActions(t, workdir, libs, hdrs)
		if err != nil {
			return nil, err
		}
		plan.Actions = append(plan.Actions, actions...)
	}
	return plan, nil
}
