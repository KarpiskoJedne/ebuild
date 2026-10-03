package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeTempYML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ebuild.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseAndPlan(t *testing.T) {
	fakeBins(t, "go")
	p := writeTempYML(t, `version: 1

project:
  name: demo
  root: .

toolchains:
  go: "1.24.0"

targets:
  - name: //lib:base
    rule: go_library
    srcs: ["lib/base.go"]

  - name: //app:svc
    rule: go_binary
    srcs: ["app/main.go"]
    deps: ["//lib:base"]
`)
	cfg, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Project != "demo" || len(cfg.Targets) != 2 {
		t.Fatalf("bad config: %+v", cfg)
	}
	if cfg.Toolchains["go"] != "1.24.0" {
		t.Fatalf("toolchains: %v", cfg.Toolchains)
	}
	plan, err := cfg.ToPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	// Dependency must come first; binary action targets its output path.
	if plan.Actions[0].Target != ".ebuild/lib/libbase.a" || plan.Actions[1].Target != ".ebuild/bin/svc" {
		t.Fatalf("order: %+v", plan.Actions)
	}
}

func TestGraphQueries(t *testing.T) {
	ts := []Target{
		{Name: "a", Deps: []string{"b", "c"}},
		{Name: "b", Deps: []string{"c"}},
		{Name: "c"},
	}
	g := NewGraph(ts)
	if got := g.TransitiveDeps("a"); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("deps: %v", got)
	}
	if got := g.ReverseDeps("c"); len(got) != 2 {
		t.Fatalf("rdeps: %v", got)
	}
	cp := g.CriticalPath("a", nil)
	if len(cp) != 3 || cp[2] != "a" {
		t.Fatalf("critical: %v", cp)
	}
	withSrc := []Target{{Name: "a", Srcs: []string{"x.go"}}, {Name: "b", Deps: []string{"a"}}}
	g2 := NewGraph(withSrc)
	if got := g2.Impact(withSrc, []string{"x.go"}); len(got) != 2 {
		t.Fatalf("impact: %v", got)
	}
}

func TestDetectLanguage(t *testing.T) {
	if DetectLanguage("m.rs") != "rust" || DetectLanguage("m.go") != "go" {
		t.Fatal("detection failed")
	}
	if DetectLanguage("noext") != "unknown" {
		t.Fatal("unknown failed")
	}
}

func TestHermeticEnv(t *testing.T) {
	os.Setenv("EBUILD_SECRET_TEST", "leak")
	defer os.Unsetenv("EBUILD_SECRET_TEST")
	env := HermeticEnv([]string{"FOO=bar"}, []string{"/x/bin"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "EBUILD_SECRET_TEST") {
		t.Fatal("host var leaked")
	}
	if !strings.Contains(joined, "FOO=bar") || !strings.Contains(joined, "PATH=/x/bin") {
		t.Fatalf("extras missing: %v", env)
	}
}

func TestSBOM(t *testing.T) {
	comps := []Component{{Name: "a", Version: "1", Kind: "target"}}
	if !strings.Contains(CycloneDX("p", comps), "CycloneDX") {
		t.Fatal("cyclonedx")
	}
	if !strings.Contains(SPDX("p", comps), "SPDXVersion") {
		t.Fatal("spdx")
	}
}

func TestFailureProb(t *testing.T) {
	if p := FailureProb(nil); p != 0.05 {
		t.Fatalf("prior: %v", p)
	}
	h := &History{Runs: 10, Failures: 9}
	if p := FailureProb(h); p < 0.5 {
		t.Fatalf("flaky not detected: %v", p)
	}
}

func TestMigrateRender(t *testing.T) {
	doc := RenderYAML("x", []Target{{Name: "//:a", Rule: "cc_binary", Srcs: []string{"a.c"}}})
	if !strings.Contains(doc, "cc_binary") || !strings.Contains(doc, "a.c") {
		t.Fatal("render")
	}
}

func TestParseCommands(t *testing.T) {
	p := writeTempYML(t, `version: 1

project:
  name: demo
  root: .

targets:
  - name: //:a
    rule: custom_script
    srcs: ["a.sh"]

commands:
  - name: test
    cmd: go test ./...
    workdir: svc
    env: ["GOFLAGS=-count=1"]
  lint: golangci-lint run
`)
	cfg, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands) != 2 {
		t.Fatalf("commands: %+v", cfg.Commands)
	}
	if cfg.Commands[0].Name != "test" || cfg.Commands[0].Cmd != "go test ./..." ||
		cfg.Commands[0].WorkDir != "svc" || len(cfg.Commands[0].Env) != 1 {
		t.Fatalf("list style: %+v", cfg.Commands[0])
	}
	if cfg.Commands[1].Name != "lint" || cfg.Commands[1].Cmd != "golangci-lint run" {
		t.Fatalf("shorthand style: %+v", cfg.Commands[1])
	}
	if _, err := FindCommand(cfg, "nope"); err == nil {
		t.Fatal("expected unknown-command error")
	}
}

func TestSplitCommand(t *testing.T) {
	argv, err := SplitCommand(`go test -run "Foo Bar" './pkg' --x=a\ b`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "test", "-run", "Foo Bar", "./pkg", "--x=a b"}
	if len(argv) != len(want) {
		t.Fatalf("split: %q", argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("split: %q want %q", argv, want)
		}
	}
	if _, err := SplitCommand(`echo "oops`); err == nil {
		t.Fatal("expected unfinished-quote error")
	}
}

func TestRunCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell fixtures")
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	cmd := CommandDef{Name: "hi", Cmd: "echo hello", Env: []string{"EBUILD_T=1"}}
	if err := RunCommand(context.Background(), dir, cmd, []string{"world"}, &out, &errb, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello world\n" {
		t.Fatalf("output: %q", out.String())
	}
	// Failing command surfaces the exit code.
	fail := CommandDef{Name: "fail", Cmd: "sh -c 'exit 3'"}
	err := RunCommand(context.Background(), dir, fail, nil, &out, &errb, nil)
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 3 {
		t.Fatalf("exit code: %v", err)
	}
	// Workdir is honored.
	mk := CommandDef{Name: "mk", Cmd: "sh -c 'pwd'", WorkDir: "."}
	out.Reset()
	if err := RunCommand(context.Background(), dir, mk, nil, &out, &errb, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != dir {
		t.Fatalf("workdir: %q want %q", out.String(), dir)
	}
}

func TestTargetHint(t *testing.T) {
	cfg := &Config{Commands: []CommandDef{{Name: "lint", Cmd: "x"}}}
	for _, tc := range []string{"//c:c", ":svc", "a:b"} {
		_, err := FindCommand(cfg, tc)
		if err == nil || !strings.Contains(err.Error(), "ebuild build") {
			t.Fatalf("hint missing for %q: %v", tc, err)
		}
	}
	if !IsTargetRef("//a:b") || IsTargetRef("lint") {
		t.Fatal("IsTargetRef")
	}
}

func TestBuiltins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix paths")
	}
	root := t.TempDir()
	var out strings.Builder
	mustWrite := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("src/a.txt", "aaa")
	mustWrite("src/sub/b.txt", "bbb")

	// copy file onto a new path (cp semantics) and into an existing dir
	if ok, err := RunBuiltin(root, "copy", []string{"src/a.txt", "dst.txt"}, &out); !ok || err != nil {
		t.Fatalf("copy: %v %v", ok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "dst.txt")); string(b) != "aaa" {
		t.Fatalf("copy result: %q", b)
	}
	if err := os.MkdirAll(filepath.Join(root, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if ok, err := RunBuiltin(root, "copy", []string{"src/a.txt", "existing"}, &out); !ok || err != nil {
		t.Fatalf("copy into dir: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "existing", "a.txt")); string(b) != "aaa" {
		t.Fatalf("copy into dir result: %q", b)
	}
	out.Reset()
	if ok, err := RunBuiltin(root, "copy", []string{"src", "bk"}, &out); !ok || err != nil {
		t.Fatalf("copy tree: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "bk", "src", "sub", "b.txt")); string(b) != "bbb" {
		t.Fatal("copy tree result")
	}
	// traversal + root-escape rejected
	for _, evil := range [][]string{{"../evil"}, {"/etc/hostname"}, {"src", ".."}} {
		if ok, err := RunBuiltin(root, "copy", evil, &out); !ok || err == nil {
			t.Fatalf("escape not rejected: %v", evil)
		}
	}
	// delete
	out.Reset()
	if ok, err := RunBuiltin(root, "delete", []string{"bk"}, &out); !ok || err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bk")); !os.IsNotExist(err) {
		t.Fatal("delete did not remove")
	}
	if ok, err := RunBuiltin(root, "delete", []string{"."}, &out); !ok || err == nil {
		t.Fatal("delete . must be refused")
	}
	// clean removes .ebuild
	mustWrite(".ebuild/cas/xx", "blob")
	out.Reset()
	if ok, err := RunBuiltin(root, "clean", nil, &out); !ok || err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ebuild")); !os.IsNotExist(err) {
		t.Fatal("clean did not remove .ebuild")
	}
	// unknown builtin name
	if ok, _ := RunBuiltin(root, "nope", nil, &out); ok {
		t.Fatal("unknown builtin claimed")
	}
	if !IsBuiltin("copy") || IsBuiltin("lint") {
		t.Fatal("IsBuiltin")
	}
}

func TestToPlanCompilerRecipes(t *testing.T) {
	fakeBins(t, "cc", "ar", "go")
	cfg := &Config{Root: ".", Targets: []Target{
		{Name: "//lib:u", Rule: "cc_library", Srcs: []string{"u.c"}, Hdrs: []string{"u.h"}},
		{Name: "//app:a", Rule: "cc_binary", Srcs: []string{"m.c"}, Deps: []string{"//lib:u"}, CFlags: []string{"-O2"}},
	}}
	plan, err := cfg.ToPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 3 { // compile u.o, archive, link binary (dep first)
		t.Fatalf("actions: %+v", plan.Actions)
	}
	if plan.Actions[0].Argv != "cc -c u.c -o .ebuild/obj/u.o -I." {
		t.Fatalf("compile: %q", plan.Actions[0].Argv)
	}
	if plan.Actions[1].Argv != "ar rcs .ebuild/lib/libu.a .ebuild/obj/u.o" {
		t.Fatalf("archive: %q", plan.Actions[1].Argv)
	}
	if plan.Actions[2].Argv != "cc -o .ebuild/bin/a m.c -O2 -I. -L.ebuild/lib -lu" {
		t.Fatalf("link: %q", plan.Actions[2].Argv)
	}
	g := &Config{Root: ".", Targets: []Target{{Name: "//svc:api", Rule: "go_binary", Srcs: []string{"svc/main.go"}}}}
	pa, err := g.ToPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pa.Actions) != 1 || pa.Actions[0].Argv != "go build -o .ebuild/bin/api ./svc" {
		t.Fatalf("go: %+v", pa.Actions)
	}
}

func TestTargetOutputAndRunnable(t *testing.T) {
	cases := map[string]string{
		"cc_binary": ".ebuild/bin/x", "cxx_binary": ".ebuild/bin/x", "go_binary": ".ebuild/bin/x",
		"rust_binary": "target/debug/x", "cc_library": ".ebuild/lib/libx.a",
	}
	for rule, want := range cases {
		if got := TargetOutput(Target{Name: "//p:x", Rule: rule}); got != want {
			t.Fatalf("%s: got %q want %q", rule, got, want)
		}
	}
	if got := TargetOutput(Target{Name: "//p:x", Rule: "cc_binary", Out: "o"}); got != "o" {
		t.Fatalf("explicit out: %q", got)
	}
	for _, r := range []string{"cc_binary", "go_binary", "rust_binary", "zig_binary", "nim_binary", "crystal_app", "ocaml_executable", "cxx_binary", "swift_binary"} {
		if !RunnableRule(r) {
			t.Fatalf("runnable: %s", r)
		}
	}
	if RunnableRule("cc_library") || RunnableRule("python_test") {
		t.Fatal("library/test must not be runnable")
	}
	cfg := &Config{Targets: []Target{{Name: "//a:b", Rule: "cc_binary"}}}
	if _, err := FindTarget(cfg, "//a:b"); err != nil {
		t.Fatal(err)
	}
	if _, err := FindTarget(cfg, "//a:nope"); err == nil {
		t.Fatal("expected no-such-target")
	}
}

func TestDoctor(t *testing.T) {
	// Outside any workspace: workspace check must fail.
	dir := t.TempDir()
	outside := filepath.Join(dir, "empty")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	checks := Doctor(outside)
	found := false
	for _, c := range checks {
		if c.Name == "workspace" && c.Status == CheckFail {
			found = true
		}
		if c.Status != CheckOK && c.Status != CheckWarn && c.Status != CheckFail {
			t.Fatalf("bad status: %+v", c)
		}
	}
	if !found {
		t.Fatalf("no workspace failure: %+v", checks)
	}
	if !HasFailures(checks) {
		t.Fatal("HasFailures")
	}
	// Valid workspace: config ok.
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "version: 1\n\nproject:\n  name: w\n  root: .\n\ntargets:\n  - name: //:a\n    rule: cc_binary\n    srcs: [\"a.c\"]\n"
	if err := os.WriteFile(filepath.Join(ws, "ebuild.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	checks = Doctor(ws)
	seen := map[string]string{}
	for _, c := range checks {
		seen[c.Name] = c.Status
	}
	if seen["workspace"] != CheckOK || seen["config"] != CheckOK {
		t.Fatalf("checks: %+v", checks)
	}
	if seen["disk"] != CheckOK {
		t.Fatalf("disk: %+v", checks)
	}
}

func TestNormalizeTargetRef(t *testing.T) {
	cases := map[string]string{
		"//c": "//c:c", "//a/b": "//a/b:b", "//c:c": "//c:c",
		"//...": "//...", ":svc": "//:svc", "plain": "plain",
		"//a:b": "//a:b",
	}
	for in, want := range cases {
		if got := NormalizeTargetRef(in); got != want {
			t.Fatalf("normalize %q: got %q want %q", in, got, want)
		}
	}
	cfg := &Config{Targets: []Target{{Name: "//c:c", Rule: "cc_binary"}}}
	if _, err := FindTarget(cfg, "//c"); err != nil {
		t.Fatalf("shorthand lookup: %v", err)
	}
}

func TestSchedulerCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	s, err := NewScheduler(2, 16, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, SampleResources())
	// `ls` of a missing dir fails with stderr; the log must be captured.
	if err := s.Submit(ctx, Task{ID: "t1", Action: Action{Target: "x", WorkDir: t.TempDir(), Argv: "ls /nonexistent-ebuild-dir-xyz"}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case r := <-s.Results():
		if r.Err == nil {
			t.Fatal("expected failure")
		}
		if !strings.Contains(r.Output, "nonexistent-ebuild-dir-xyz") {
			t.Fatalf("output not captured: %q", r.Output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result")
	}
}

func TestSelectActionsAll(t *testing.T) {
	fakeBins(t, "cc", "ar")
	cfg := &Config{Targets: []Target{
		{Name: "//a:a", Rule: "cc_binary", Srcs: []string{"x.c"}},
		{Name: "//b:y", Rule: "cc_library", Srcs: []string{"y.c"}},
	}}
	plan, err := cfg.ToPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, wants := range [][]string{{"all"}, {"//..."}, {}} {
		acts, err := SelectActions(cfg, plan, wants)
		if err != nil || len(acts) != len(plan.Actions) {
			t.Fatalf("wants %v: %d actions, err %v", wants, len(acts), err)
		}
	}
	acts, err := SelectActions(cfg, plan, []string{"//a"})
	if err != nil || len(acts) != 1 || acts[0].Name != "//a:a" {
		t.Fatalf("shorthand select: %+v %v", acts, err)
	}
	if _, err := SelectActions(cfg, plan, []string{"//nope"}); err == nil {
		t.Fatal("expected no-such-target")
	}
}

func TestObjectPath(t *testing.T) {
	if got := ObjectPath("app/main.c"); got != ".ebuild/obj/app/main.o" {
		t.Fatalf("obj: %q", got)
	}
	if got := TargetOutput(Target{Name: "//a:b", Rule: "cc_library"}); got != ".ebuild/lib/libb.a" {
		t.Fatalf("lib: %q", got)
	}
}

func TestPluginForTool(t *testing.T) {
	if p, ok := PluginForTool("go"); !ok || p != "golang" {
		t.Fatalf("go: %q %v", p, ok)
	}
	if _, ok := PluginForTool("cc"); ok {
		t.Fatal("cc must not map to a plugin")
	}
}

func TestListInstalledFakeAsdf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"plugin\" ]; then printf 'golang\\nnodejs\\n'; elif [ \"$1\" = \"list\" ]; then printf '  1.23.1\\n* 1.24.0\\nNo versions installed\\n'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "asdf"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// HasCLI needs the real lookup; ListInstalled exercises parsing.
	got, err := ListInstalled("golang")
	if err != nil {
		if strings.Contains(err.Error(), "asdf CLI not found") {
			t.Skip("asdf shadowed")
		}
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "1.24.0" || got[1] != "1.23.1" {
		t.Fatalf("parsed: %q", got)
	}
}

func TestAllRecipesExpand(t *testing.T) {
	// Deterministic backend probing regardless of host toolchains: stub
	// every probed binary (expand-only test, nothing is executed).
	fakeBins(t, "pip", "pytest", "trivy", "docker", "pdflatex", "esbuild",
		"cc", "ar", "cargo", "rustc", "go", "javac", "jar", "kotlinc", "scalac",
		"swiftc", "dotnet", "cabal", "stack", "ghc", "dune", "mix", "rebar3",
		"clojure", "gem", "composer", "julia", "R", "zig", "nim", "crystal",
		"tsc", "webpack", "vite", "protoc", "openapi-generator-cli",
		"graphql-codegen", "tailwindcss", "mvn", "gradle", "sbt",
		"ansible-playbook", "ansible-lint", "sh", "python3", "helm", "terraform")
	c := func(rule string) Target {
		return Target{
			Name: "//p:x", Rule: rule,
			Srcs: []string{"a.c", "b.cpp", "c.java", "d.kt", "e.swift", "f.scala", "g.go", "h.py", "i.ts", "j.js", "k.rs", "l.rb", "m.php", "n.lua", "o.m", "p.r", "q.jl", "r.sh", "s.proto", "t.yaml", "u.css", "v.zig", "w.nim", "x.cr", "y.tex", "z.graphql", "spec.yml", "api.openapi.yml", "play.yml", "chart", "main.zig", "main.nim", "main.cr", "main.jl", "doc.tex", "app.gemspec", "build.gradle", "project.clj", "pkg.cabal", "dune.exe", "mix.exs", "rebar.config", "Dockerfile", "main.tf", "Gemfile",
				"test_x.py", "a_test.go",
			},
			Hdrs:       []string{"a.h"},
			Languages:  []string{"go", "python"},
			Tags:       []string{"img:latest"},
			Generator:  "go",
			Entry:      "main.ts",
			Exe:        "bin/main.exe",
			Cmd:        "echo hi",
			Dockerfile: "Dockerfile",
			Context:    ".",
			Dir:        ".",
			Main:       "com.Main",
		}
	}
	for _, rule := range validRules {
		wd := "."
		if rule == "rust_binary" || rule == "rust_library" {
			wd = t.TempDir()
			if err := os.WriteFile(filepath.Join(wd, "Cargo.toml"), []byte("[package]\nname=\"x\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		acts, err := ruleActions(c(rule), wd, map[string]string{}, nil)
		if err != nil {
			t.Fatalf("%s: %v", rule, err)
		}
		if len(acts) == 0 {
			t.Fatalf("%s: no actions", rule)
		}
		for _, a := range acts {
			if a.Rule == "" || a.Target == "" || a.Argv == "" {
				t.Fatalf("%s: incomplete action %+v", rule, a)
			}
		}
	}
	// Unknown rule and bad toolchain fail fast.
	if _, err := ruleActions(Target{Name: "//x", Rule: "cobol_program"}, ".", nil, nil); err == nil {
		t.Fatal("expected unknown-rule error")
	}
	if _, err := ruleActions(Target{Name: "//x", Rule: "cc_binary", Toolchain: "cobol"}, ".", nil, nil); err == nil {
		t.Fatal("expected unknown-toolchain error")
	}
	if _, err := ruleActions(Target{Name: "//x", Rule: "custom_script"}, ".", nil, nil); err == nil {
		t.Fatal("expected empty-cmd error")
	}
}

func TestRecipeNeedsEdges(t *testing.T) {
	fakeBins(t, "cc", "ar", "javac", "jar")
	libs := map[string]string{"//lib:base": ".ebuild/lib/libbase.a", "//j:jl": "dist/jl.jar"}
	// C archive needs its objects.
	acts, err := ruleActions(Target{Name: "//lib:base", Rule: "cc_library", Srcs: []string{"u.c"}}, ".", libs, nil)
	if err != nil || len(acts) != 2 {
		t.Fatalf("lib: %+v %v", acts, err)
	}
	if len(acts[1].Needs) != 1 || acts[1].Needs[0] != acts[0].Target {
		t.Fatalf("archive needs: %+v", acts[1])
	}
	// C binary links dep libs.
	acts, err = ruleActions(Target{Name: "//app:a", Rule: "cc_binary", Srcs: []string{"m.c"}, Deps: []string{"//lib:base"}}, ".", libs, map[string][]string{"//lib:base": {"u.h"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(acts[0].Needs) != 1 || !strings.Contains(acts[0].Argv, "-lbase") || !strings.Contains(acts[0].Argv, "-I.") {
		t.Fatalf("link: %+v", acts[0])
	}
	// Java binary needs dep jars on the classpath.
	acts, err = ruleActions(Target{Name: "//j:app", Rule: "java_binary", Srcs: []string{"A.java"}, Deps: []string{"//j:jl"}, Main: "A"}, ".", libs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 2 || len(acts[0].Needs) != 1 || !strings.Contains(acts[0].Argv, "-cp dist/jl.jar") {
		t.Fatalf("java: %+v", acts)
	}
	if !strings.Contains(acts[1].Argv, "--main-class A") {
		t.Fatalf("jar: %+v", acts[1])
	}
}

func TestRunPlanWaves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "stage1.done")
	stage2 := filepath.Join(dir, "stage2.done")
	actions := []Action{
		{Name: "s1", Rule: "gen", Target: marker, WorkDir: dir, Argv: "touch " + marker},
		{Name: "s2", Rule: "gen", Target: stage2, WorkDir: dir, Argv: "touch " + stage2, Needs: []string{marker}},
	}
	results, err := RunPlan(dir, actions, 2)
	if err != nil {
		t.Fatalf("waves: %v", results)
	}
	if len(results) != 2 {
		t.Fatalf("results: %+v", results)
	}
	// Failing dependency skips the dependent with a clear error.
	bad := []Action{
		{Name: "f1", Rule: "gen", Target: filepath.Join(dir, "never"), WorkDir: dir, Argv: "false"},
		{Name: "f2", Rule: "gen", Target: filepath.Join(dir, "never2"), WorkDir: dir, Argv: "true", Needs: []string{filepath.Join(dir, "never")}},
	}
	results, err = RunPlan(dir, bad, 2)
	if err == nil {
		t.Fatalf("expected failure: %+v", results)
	}
	skipped := false
	for _, r := range results {
		if r.Err != nil && strings.Contains(r.Err.Error(), "skipped") {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("no skip recorded: %+v", results)
	}
}

// fakeBins creates executable stub files (never executed, only LookPath'd,
// except where noted) and prepends the dir to PATH.
func fakeBins(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestToolchainFallbacks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	// Only pip available -> python_package uses pip.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pip"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // nothing else: no uv/pip2/poetry/python3
	acts, err := ruleActions(Target{Name: "//p:x", Rule: "python_package"}, ".", nil, nil)
	if err != nil {
		t.Fatalf("pip fallback: %v", err)
	}
	if !strings.HasPrefix(acts[0].Argv, "pip wheel") {
		t.Fatalf("argv: %q", acts[0].Argv)
	}
	// pytest preferred over unittest when present.
	if err := os.WriteFile(filepath.Join(dir, "pytest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	acts, err = ruleActions(Target{Name: "//p:t", Rule: "python_test", Srcs: []string{"tests"}}, ".", nil, nil)
	if err != nil || !strings.Contains(acts[0].Argv, "pytest") {
		t.Fatalf("pytest: %+v %v", acts, err)
	}
	// Nothing at all -> actionable install hint.
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	if _, err := ruleActions(Target{Name: "//p:x", Rule: "python_package"}, ".", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "none on PATH") {
		t.Fatalf("hint: %v", err)
	}
	if _, err := ruleActions(Target{Name: "//p:x", Rule: "security_scan"}, ".", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "trivy|grype|semgrep") {
		t.Fatalf("hint: %v", err)
	}
}

func TestExplicitToolchainHonored(t *testing.T) {
	fakeBins(t, "pip", "poetry")
	acts, err := ruleActions(Target{Name: "//p:x", Rule: "python_package", Toolchain: "poetry"}, ".", nil, nil)
	if err != nil || !strings.HasPrefix(acts[0].Argv, "poetry build") {
		t.Fatalf("explicit: %+v %v", acts, err)
	}
}

func TestSchedulerShellQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	dir := t.TempDir()
	// Quoted multi-word argument must survive as one argv element,
	// and `sh -c '...'` chains must execute through a shell.
	actions := []Action{
		{Name: "q", Rule: "gen", Target: filepath.Join(dir, "quoted.txt"), WorkDir: dir,
			Argv: "sh -c 'printf \"a b\" > quoted.txt'"},
		{Name: "m", Rule: "gen", Target: filepath.Join(dir, "m.ok"), WorkDir: dir,
			Argv: "true", Needs: []string{filepath.Join(dir, "quoted.txt")}},
	}
	results, err := RunPlan(dir, actions, 2)
	if err != nil {
		t.Fatalf("shell quoting: %v %+v", err, results)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "quoted.txt"))
	if string(b) != "a b" {
		t.Fatalf("quoting: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "m.ok")); err != nil {
		t.Fatal("scheduler did not stamp .ok marker")
	}
}

func TestSchedulerVerifiesArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	dir := t.TempDir()
	actions := []Action{
		{Name: "ghost", Rule: "gen", Target: filepath.Join(dir, "never-created.bin"), WorkDir: dir, Argv: "true"},
	}
	results, err := RunPlan(dir, actions, 1)
	if err == nil {
		t.Fatal("phantom output accepted")
	}
	found := false
	for _, r := range results {
		if r.Err != nil && strings.Contains(r.Err.Error(), "produced no output") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no per-action diagnostic: %+v", results)
	}
}

func TestRuleCatalogComplete(t *testing.T) {
	rules := ValidRules()
	if len(rules) != 50 {
		t.Fatalf("catalog: %d rules, want 50", len(rules))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r] {
			t.Fatalf("duplicate rule %q", r)
		}
		seen[r] = true
		if RuleDescription(r) == "" {
			t.Fatalf("no description for %q", r)
		}
		// Every rule except custom_script declares at least one toolchain.
		if r != "custom_script" && len(ToolchainsForRule(r)) == 0 {
			t.Fatalf("no toolchains for %q", r)
		}
	}
	if len(ToolchainsForRule("nope")) != 0 || RuleDescription("nope") != "" {
		t.Fatal("unknown rule metadata")
	}
}

func TestPythonBuildModuleProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	// Fake python3 whose `import build` fails: build backend unavailable.
	dir := t.TempDir()
	py := "#!/bin/sh\nif [ \"$2\" = \"import build\" ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(py), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	_, err := ruleActions(Target{Name: "//p:x", Rule: "python_package"}, ".", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "pip install build") {
		t.Fatalf("probe: %v", err)
	}
}
