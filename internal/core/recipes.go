// recipes: local build recipes for every ebuild rule.
//
// Each recipe mirrors its Lua counterpart in rules/ (which the native daemon
// evaluates for sandboxed execution) but emits directly executable argv for
// the Go plane, with Needs edges so the scheduler builds dependencies first.
// Target.Toolchain selects the toolchain variant; unknown toolchains and
// rules fail fast with the valid set instead of producing garbage argv.
package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// validRules lists every rule the Go plane can expand.
var validRules = []string{
	"cc_binary", "cc_library", "cxx_binary", "cxx_library",
	"rust_binary", "rust_library", "go_binary", "go_library",
	"python_package", "python_test", "typescript_app", "typescript_library",
	"java_binary", "java_library", "kotlin_app", "kotlin_library",
	"swift_binary", "swift_library", "scala_app", "scala_library",
	"protobuf_library", "grpc_service", "docker_image", "helm_chart",
	"terraform_apply", "ansible_playbook", "shell_script", "ruby_gem",
	"php_composer", "csharp_assembly", "fsharp_assembly", "clojure_project",
	"haskell_package", "ocaml_executable", "zig_binary", "nim_binary",
	"crystal_app", "elixir_mix", "erlang_otp", "julia_script",
	"r_package", "latex_document", "openapi_codegen", "graphql_schema",
	"tailwind_css", "webpack_bundle", "vite_build", "ansible_lint",
	"security_scan", "custom_script",
}

func isValidRule(r string) bool {
	for _, v := range validRules {
		if v == r {
			return true
		}
	}
	return false
}

// ValidRules returns all buildable rule names (stable order).
func ValidRules() []string { return append([]string{}, validRules...) }

// ruleDescriptions gives one-line picker help per rule.
var ruleDescriptions = map[string]string{
	"cc_binary": "C executable", "cc_library": "C static library",
	"cxx_binary": "C++ executable", "cxx_library": "C++ static library",
	"rust_binary": "Rust binary (cargo)", "rust_library": "Rust rlib",
	"go_binary": "Go binary", "go_library": "Go package",
	"python_package": "Python wheel", "python_test": "Python tests",
	"typescript_app": "TS app bundle", "typescript_library": "TS declarations",
	"java_binary": "Java app jar", "java_library": "Java library jar",
	"kotlin_app": "Kotlin app jar", "kotlin_library": "Kotlin library jar",
	"swift_binary": "Swift executable", "swift_library": "Swift module",
	"scala_app": "Scala app jar", "scala_library": "Scala library jar",
	"protobuf_library": "protoc stubs", "grpc_service": "gRPC stubs",
	"docker_image": "container image", "helm_chart": "Helm package",
	"terraform_apply": "Terraform stack", "ansible_playbook": "Ansible run",
	"shell_script": "shell script", "ruby_gem": "Ruby gem",
	"php_composer": "Composer install", "csharp_assembly": ".NET (C#) build",
	"fsharp_assembly": ".NET (F#) build", "clojure_project": "Clojure uberjar",
	"haskell_package": "Cabal package", "ocaml_executable": "dune executable",
	"zig_binary": "Zig binary", "nim_binary": "Nim binary",
	"crystal_app": "Crystal app", "elixir_mix": "Mix project",
	"erlang_otp": "OTP app", "julia_script": "Julia script",
	"r_package": "R package", "latex_document": "LaTeX PDF",
	"openapi_codegen": "OpenAPI client", "graphql_schema": "GraphQL codegen",
	"tailwind_css": "Tailwind CSS", "webpack_bundle": "webpack bundle",
	"vite_build": "Vite build", "ansible_lint": "Ansible lint",
	"security_scan": "vuln scan", "custom_script": "arbitrary command",
}

// RuleDescription returns one-line help for a rule ("" if unknown).
func RuleDescription(rule string) string { return ruleDescriptions[rule] }

// ruleToolchains maps each rule to its toolchain variants; empty default
// means "first available backend" (auto-fallback at plan time).
var ruleToolchains = map[string][]string{
	"cc_binary":   {"cc", "gcc", "clang", "tcc", "cl"},
	"cc_library":  {"cc", "gcc", "clang", "tcc", "cl"},
	"cxx_binary":  {"c++", "g++", "clang++", "cl"},
	"cxx_library": {"c++", "g++", "clang++", "cl"},
	"rust_binary": {"cargo", "rustc"}, "rust_library": {"cargo", "rustc"},
	"go_binary": {"go"}, "go_library": {"go"},
	"python_package":     {"uv", "pip", "poetry", "build"},
	"python_test":        {"pytest", "unittest"},
	"typescript_app":     {"esbuild", "tsc", "webpack", "vite"},
	"typescript_library": {"tsc"},
	"java_binary":        {"javac", "maven", "gradle"},
	"java_library":       {"javac", "maven", "gradle"},
	"kotlin_app":         {"kotlinc", "gradle"}, "kotlin_library": {"kotlinc", "gradle"},
	"swift_binary": {"swiftc"}, "swift_library": {"swiftc"},
	"scala_app": {"scalac", "sbt"}, "scala_library": {"scalac", "sbt"},
	"protobuf_library": {"protoc"}, "grpc_service": {"protoc"},
	"docker_image": {"docker", "podman"}, "helm_chart": {"helm"},
	"terraform_apply":  {"terraform"},
	"ansible_playbook": {"ansible-playbook"}, "ansible_lint": {"ansible-lint"},
	"shell_script": {"sh", "bash", "dash", "zsh"},
	"ruby_gem":     {"gem", "bundler"}, "php_composer": {"composer"},
	"csharp_assembly": {"dotnet"}, "fsharp_assembly": {"dotnet"},
	"clojure_project": {"clojure"}, "haskell_package": {"cabal", "stack", "ghc"},
	"ocaml_executable": {"dune"}, "zig_binary": {"zig"},
	"nim_binary": {"nim"}, "crystal_app": {"crystal"},
	"elixir_mix": {"mix"}, "erlang_otp": {"rebar3"},
	"julia_script": {"julia"}, "r_package": {"R"},
	"latex_document":  {"pdflatex", "xelatex", "lualatex", "latexmk"},
	"openapi_codegen": {"openapi-generator-cli"},
	"graphql_schema":  {"graphql-codegen"},
	"tailwind_css":    {"tailwindcss"},
	"webpack_bundle":  {"webpack"}, "vite_build": {"vite"},
	"security_scan": {"trivy", "grype", "semgrep"},
	"custom_script": {},
}

// ToolchainsForRule returns the valid toolchain variants for a rule
// (empty slice = none, e.g. custom_script).
func ToolchainsForRule(rule string) []string {
	return append([]string{}, ruleToolchains[rule]...)
}

// recipeCtx carries cross-target link information.
type recipeCtx struct {
	workdir string
	// libs maps dep target name -> built artifact path.
	libs map[string]string
	// hdrs maps dep target name -> its header files (for -I inference).
	hdrs map[string][]string
}

// depHdrs collects header files of linked library deps.
func depHdrs(t Target, ctx recipeCtx) []string {
	var out []string
	for _, d := range t.Deps {
		out = append(out, ctx.hdrs[d]...)
	}
	return out
}

func shortOf(t Target) string { return shortName(t.Name) }

// incFlags returns sorted, deduplicated -I<dir> (or prefix+dir) flags for
// the source and header directories of a target plus extra header files
// (typically the hdrs of linked libraries), so headers in sibling
// directories (e.g. include/) resolve without manual configuration.
func incFlags(t Target, prefix string, extra ...[]string) string {
	seen := map[string]bool{}
	var dirs []string
	all := append(append([]string{}, t.Srcs...), t.Hdrs...)
	for _, e := range extra {
		all = append(all, e...)
	}
	for _, f := range all {
		d := filepath.ToSlash(filepath.Dir(f))
		if d == "" {
			d = "."
		}
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	var out []string
	for _, d := range dirs {
		out = append(out, prefix+d)
	}
	return strings.Join(out, " ")
}

func archiver(driver string) string {
	if driver == "cl" {
		return "lib"
	}
	return "ar"
}

// linkInputs resolves library deps to Needs edges plus "-L<dir> -l<name>"
// linker flags (libfoo.a -> -lfoo, deduplicated, sorted for determinism).
func linkInputs(t Target, libs map[string]string) (needs []string, flags string) {
	seenDir := map[string]bool{}
	seenLib := map[string]bool{}
	var ldirs, lnames []string
	for _, d := range t.Deps {
		out, ok := libs[d]
		if !ok {
			continue
		}
		needs = append(needs, out)
		dir := filepath.ToSlash(filepath.Dir(out))
		base := filepath.Base(out)
		name := strings.TrimSuffix(strings.TrimPrefix(base, "lib"), ".a")
		name = strings.TrimSuffix(name, ".lib")
		if name == "" {
			name = base
		}
		if !seenDir[dir] {
			seenDir[dir] = true
			ldirs = append(ldirs, dir)
		}
		if !seenLib[name] {
			seenLib[name] = true
			lnames = append(lnames, name)
		}
	}
	sort.Strings(ldirs)
	sort.Strings(lnames)
	var parts []string
	for _, d := range ldirs {
		parts = append(parts, "-L"+d)
	}
	for _, n := range lnames {
		parts = append(parts, "-l"+n)
	}
	return needs, strings.Join(parts, " ")
}

// marker returns a stamp path under .ebuild/<area>/ proving a side-effect
// action ran; recipes append `&& touch <marker>`.
func marker(t Target, area string) string {
	if t.Out != "" {
		return t.Out
	}
	return ".ebuild/" + area + "/" + shortOf(t) + ".ok"
}

// entryOf resolves the entry file for bundling rules.
func entryOf(t Target) (string, error) {
	if t.Entry != "" {
		return t.Entry, nil
	}
	if len(t.Srcs) > 0 {
		return t.Srcs[0], nil
	}
	return "", fmt.Errorf("ebuild: rule %s needs srcs or entry", t.Rule)
}

func needSrcs(t Target, what string) error {
	if len(t.Srcs) == 0 {
		return fmt.Errorf("ebuild: rule %s (%s) needs srcs", t.Rule, what)
	}
	return nil
}

func act(t Target, workdir, rule, target, argv string, needs []string) Action {
	return Action{Name: t.Name, Rule: rule, Target: target, WorkDir: workdir, Argv: argv, Needs: needs}
}

// ---------- C / C++ ----------

func cDriver(t Target, cxx bool) (string, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "cc"
		if cxx {
			tc = "c++"
		}
	}
	switch tc {
	case "cc", "gcc", "clang", "tcc":
		if cxx && (tc == "cc" || tc == "gcc" || tc == "tcc") {
			return "", fmt.Errorf("ebuild: toolchain %q cannot compile C++ (use c++|g++|clang++)", tc)
		}
		if !cxx && tc == "tcc" {
			return "tcc", nil
		}
		if cxx {
			if tc == "cc" {
				tc = "c++"
			}
			return tc + " -std=c++17", nil
		}
		return tc, nil
	case "g++", "c++", "clang++":
		if !cxx {
			return "", fmt.Errorf("ebuild: toolchain %q is a C++ compiler, use it with cxx_* rules", tc)
		}
		return tc + " -std=c++17", nil
	case "cl", "msvc":
		return "cl", nil
	}
	return "", fmt.Errorf("ebuild: unknown C toolchain %q (cc|gcc|clang|tcc|c++|g++|clang++|cl)", tc)
}

func recipeCC(t Target, ctx recipeCtx, cxx bool) ([]Action, error) {
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	driver, err := cDriver(t, cxx)
	if err != nil {
		return nil, err
	}
	out := TargetOutput(t)
	flags := strings.Join(t.CFlags, " ")
	if driver == "cl" {
		// MSVC flag dialect: /c /Fo /Fe /I /link /LIBPATH.
		var objs []string
		var actions []Action
		for _, s := range t.Srcs {
			o := ObjectPath(s) + ".obj"
			objs = append(objs, o)
			cmd := "cl /nologo /c " + s + " /Fo" + o
			for _, f := range strings.Fields(incFlags(t, "/I", depHdrs(t, ctx))) {
				cmd += " " + f
			}
			if flags != "" {
				cmd += " " + flags
			}
			actions = append(actions, act(t, ctx.workdir, "cc", o, cmd, nil))
		}
		needs, _ := linkInputs(t, ctx.libs)
		link := "link /nologo /OUT:" + out + " " + strings.Join(objs, " ")
		for _, n := range needs {
			link += " " + n
		}
		actions = append(actions, act(t, ctx.workdir, "link", out, link, append(objs, needs...)))
		return actions, nil
	}
	if err := needBin(t.Rule, strings.Fields(driver)[0]); err != nil {
		return nil, err
	}
	needs, ld := linkInputs(t, ctx.libs)
	argv := driver + " -o " + out + " " + strings.Join(t.Srcs, " ")
	if flags != "" {
		argv += " " + flags
	}
	if inc := incFlags(t, "-I", depHdrs(t, ctx)); inc != "" {
		argv += " " + inc
	}
	if ld != "" {
		argv += " " + ld
	}
	return []Action{act(t, ctx.workdir, "cc", out, argv, needs)}, nil
}

func recipeCLib(t Target, ctx recipeCtx, cxx bool) ([]Action, error) {
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	driver, err := cDriver(t, cxx)
	if err != nil {
		return nil, err
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, strings.Fields(driver)[0]); err != nil {
		return nil, err
	}
	if err := needBin(t.Rule, archiver(driver)); err != nil {
		return nil, err
	}
	flags := strings.Join(t.CFlags, " ")
	var actions []Action
	var objs []string
	for _, s := range t.Srcs {
		o := ObjectPath(s)
		objs = append(objs, o)
		var cmd string
		if driver == "cl" {
			cmd = "cl /nologo /c " + s + " /Fo" + o
			for _, f := range strings.Fields(incFlags(t, "/I", depHdrs(t, ctx))) {
				cmd += " " + f
			}
		} else {
			cmd = driver + " -c " + s + " -o " + o
			if inc := incFlags(t, "-I", depHdrs(t, ctx)); inc != "" {
				cmd += " " + inc
			}
		}
		if flags != "" {
			cmd += " " + flags
		}
		actions = append(actions, act(t, ctx.workdir, "cc", o, cmd, nil))
	}
	arch := "ar rcs " + out + " " + strings.Join(objs, " ")
	if driver == "cl" {
		arch = "lib /nologo /OUT:" + out + " " + strings.Join(objs, " ")
	}
	actions = append(actions, act(t, ctx.workdir, "archive", out, arch, objs))
	return actions, nil
}

// ---------- Rust ----------

func recipeRust(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "cargo"
	}
	out := TargetOutput(t)
	switch tc {
	case "cargo":
		if err := needBin(t.Rule, "cargo"); err != nil {
			return nil, err
		}
		manifest, err := findManifest(ctx.workdir, t, "Cargo.toml")
		if err != nil {
			return nil, err
		}
		argv := "cargo build --manifest-path " + manifest
		if lib {
			argv += " --lib"
		}
		return []Action{act(t, ctx.workdir, "cargo", out, argv, nil)}, nil
	case "rustc":
		if err := needBin(t.Rule, "rustc"); err != nil {
			return nil, err
		}
		if err := needSrcs(t, t.Name); err != nil {
			return nil, err
		}
		argv := "rustc --edition=2021 -o " + out + " " + strings.Join(t.Srcs, " ")
		var needs []string
		for _, d := range t.Deps {
			if rlib, ok := ctx.libs[d]; ok {
				needs = append(needs, rlib)
				argv += " --extern " + depShort(d) + "=" + rlib
			}
		}
		return []Action{act(t, ctx.workdir, "rustc", out, argv, needs)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown Rust toolchain %q (cargo|rustc)", tc)
}

func depShort(dep string) string { return shortName(dep) }

// findManifest walks up from the first src dir looking for a manifest file.
func findManifest(workdir string, t Target, name string) (string, error) {
	start := workdir
	if len(t.Srcs) > 0 {
		start = filepath.Join(workdir, filepath.Dir(t.Srcs[0]))
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	root, _ := filepath.Abs(workdir)
	for {
		if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
			rel, _ := filepath.Rel(root, filepath.Join(abs, name))
			if rel == "" {
				return name, nil
			}
			return filepath.ToSlash(rel), nil
		}
		parent := filepath.Dir(abs)
		if parent == abs || (root != "" && len(abs) < len(root)) {
			break
		}
		abs = parent
	}
	return "", fmt.Errorf("ebuild: no %s found above %s", name, start)
}

// ---------- Go ----------

func goPkg(t Target) string {
	if len(t.Srcs) == 0 {
		return "."
	}
	if d := filepath.Dir(t.Srcs[0]); d != "." && d != "" {
		return "./" + filepath.ToSlash(d)
	}
	return "."
}

func recipeGo(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "go" {
		return nil, fmt.Errorf("ebuild: unknown Go toolchain %q (go)", tc)
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, "go"); err != nil {
		return nil, err
	}
	argv := "go build -o " + out + " " + goPkg(t)
	return []Action{act(t, ctx.workdir, "go-build", out, argv, nil)}, nil
}

// ---------- Java / Kotlin / Scala ----------

func javaClasses(t Target) string { return ".ebuild/classes/" + shortOf(t) }

func recipeJava(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "javac"
	}
	out := TargetOutput(t)
	switch tc {
	case "javac":
		for _, b := range []string{"javac", "jar"} {
			if err := needBin(t.Rule, b); err != nil {
				return nil, err
			}
		}
		if err := needSrcs(t, t.Name); err != nil {
			return nil, err
		}
		cls := javaClasses(t)
		var cp []string
		var needs []string
		for _, d := range t.Deps {
			if jar, ok := ctx.libs[d]; ok {
				needs = append(needs, jar)
				cp = append(cp, jar)
			}
		}
		compile := "javac -d " + cls + " " + strings.Join(t.Srcs, " ")
		if len(cp) > 0 {
			compile = "javac -cp " + strings.Join(cp, ":") + " -d " + cls + " " + strings.Join(t.Srcs, " ")
		}
		actions := []Action{act(t, ctx.workdir, "javac", cls, compile, needs)}
		if lib || t.Main == "" {
			actions = append(actions, act(t, ctx.workdir, "archive", out,
				"jar --create --file "+out+" -C "+cls+" .", []string{cls}))
		} else {
			actions = append(actions, act(t, ctx.workdir, "archive", out,
				"jar --create --file "+out+" --main-class "+t.Main+" -C "+cls+" .", []string{cls}))
		}
		return actions, nil
	case "maven":
		if err := needBin(t.Rule, "mvn"); err != nil {
			return nil, err
		}
		dir := t.Dir
		if dir == "" {
			dir = "."
		}
		stamp := marker(t, "maven")
		argv := "sh -c 'cd " + dir + " && mvn -q -DskipTests package'"
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	case "gradle":
		if err := needBin(t.Rule, "gradle"); err != nil {
			return nil, err
		}
		dir := t.Dir
		if dir == "" {
			dir = "."
		}
		stamp := marker(t, "gradle")
		argv := "sh -c 'cd " + dir + " && gradle build -x test'"
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown Java toolchain %q (javac|maven|gradle)", tc)
}

func recipeKotlin(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "kotlinc"
	}
	out := TargetOutput(t)
	switch tc {
	case "kotlinc":
		if err := needBin(t.Rule, "kotlinc"); err != nil {
			return nil, err
		}
		if err := needSrcs(t, t.Name); err != nil {
			return nil, err
		}
		argv := "kotlinc " + strings.Join(t.Srcs, " ") + " -d " + out
		var cp []string
		var needs []string
		for _, d := range t.Deps {
			if jar, ok := ctx.libs[d]; ok {
				needs = append(needs, jar)
				cp = append(cp, jar)
			}
		}
		if len(cp) > 0 {
			argv += " -cp " + strings.Join(cp, ":")
		}
		_ = lib
		return []Action{act(t, ctx.workdir, "kotlinc", out, argv, needs)}, nil
	case "gradle":
		stamp := marker(t, "gradle")
		argv := "sh -c 'cd " + orDot(t.Dir) + " && gradle build -x test'"
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown Kotlin toolchain %q (kotlinc|gradle)", tc)
}

func recipeScala(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "scalac"
	}
	out := TargetOutput(t)
	switch tc {
	case "scalac":
		for _, b := range []string{"scalac", "jar"} {
			if err := needBin(t.Rule, b); err != nil {
				return nil, err
			}
		}
		if err := needSrcs(t, t.Name); err != nil {
			return nil, err
		}
		cls := javaClasses(t)
		var cp []string
		var needs []string
		for _, d := range t.Deps {
			if jar, ok := ctx.libs[d]; ok {
				needs = append(needs, jar)
				cp = append(cp, jar)
			}
		}
		compile := "scalac -d " + cls + " " + strings.Join(t.Srcs, " ")
		if len(cp) > 0 {
			compile = "scalac -cp " + strings.Join(cp, ":") + " -d " + cls + " " + strings.Join(t.Srcs, " ")
		}
		_ = lib
		return []Action{
			act(t, ctx.workdir, "gen", cls, compile, needs),
			act(t, ctx.workdir, "archive", out, "jar --create --file "+out+" -C "+cls+" .", []string{cls}),
		}, nil
	case "sbt":
		if err := needBin(t.Rule, "sbt"); err != nil {
			return nil, err
		}
		stamp := marker(t, "sbt")
		argv := "sh -c 'cd " + orDot(t.Dir) + " && sbt package'"
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown Scala toolchain %q (scalac|sbt)", tc)
}

// ---------- Swift / .NET ----------

func recipeSwift(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "swiftc" {
		return nil, fmt.Errorf("ebuild: unknown Swift toolchain %q (swiftc)", tc)
	}
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, "swiftc"); err != nil {
		return nil, err
	}
	var argv string
	if lib {
		argv = "swiftc -emit-library -o " + out + " " + strings.Join(t.Srcs, " ")
	} else {
		argv = "swiftc -o " + out + " " + strings.Join(t.Srcs, " ")
	}
	needs, ld := linkInputs(t, ctx.libs)
	if ld != "" {
		argv += " " + ld
	}
	return []Action{act(t, ctx.workdir, "swiftc", out, argv, needs)}, nil
}

func recipeDotnet(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "dotnet" {
		return nil, fmt.Errorf("ebuild: unknown .NET toolchain %q (dotnet)", tc)
	}
	proj := t.Dir
	if proj == "" && len(t.Srcs) > 0 {
		proj = filepath.Dir(t.Srcs[0])
	}
	proj = orDot(proj)
	if err := needBin(t.Rule, "dotnet"); err != nil {
		return nil, err
	}
	stamp := marker(t, "dotnet")
	argv := "dotnet build " + proj + " -c Release --nologo"
	return []Action{act(t, ctx.workdir, "csc", stamp, argv, nil)}, nil
}

// ---------- Haskell / OCaml / ML family ----------

func recipeHaskell(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "cabal"
	}
	out := TargetOutput(t)
	switch tc {
	case "cabal":
		if err := needBin(t.Rule, "cabal"); err != nil {
			return nil, err
		}
		stamp := marker(t, "cabal")
		argv := "cabal build " + orDot(t.Dir)
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	case "stack":
		if err := needBin(t.Rule, "stack"); err != nil {
			return nil, err
		}
		stamp := marker(t, "stack")
		argv := "sh -c 'cd " + orDot(t.Dir) + " && stack build'"
		return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
	case "ghc":
		if err := needBin(t.Rule, "ghc"); err != nil {
			return nil, err
		}
		if err := needSrcs(t, t.Name); err != nil {
			return nil, err
		}
		return []Action{act(t, ctx.workdir, "gen",
			out, "ghc -o "+out+" "+strings.Join(t.Srcs, " "), nil)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown Haskell toolchain %q (cabal|stack|ghc)", tc)
}

func recipeOCaml(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "dune" {
		return nil, fmt.Errorf("ebuild: unknown OCaml toolchain %q (dune)", tc)
	}
	exe := t.Exe
	if exe == "" {
		exe = t.Out
	}
	if err := needBin(t.Rule, "dune"); err != nil {
		return nil, err
	}
	out := TargetOutput(t)
	argv := "dune build"
	if exe != "" {
		argv += " " + exe
	}
	return []Action{act(t, ctx.workdir, "gen", out, argv, nil)}, nil
}

func recipeElixir(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "mix" {
		return nil, fmt.Errorf("ebuild: unknown Elixir toolchain %q (mix)", tc)
	}
	if err := needBin(t.Rule, "mix"); err != nil {
		return nil, err
	}
	stamp := marker(t, "mix")
	argv := "sh -c 'cd " + orDot(t.Dir) + " && mix compile'"
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeErlang(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "rebar3" {
		return nil, fmt.Errorf("ebuild: unknown Erlang toolchain %q (rebar3)", tc)
	}
	if err := needBin(t.Rule, "rebar3"); err != nil {
		return nil, err
	}
	stamp := marker(t, "otp")
	argv := "sh -c 'cd " + orDot(t.Dir) + " && rebar3 compile'"
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeClojure(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "clojure" {
		return nil, fmt.Errorf("ebuild: unknown Clojure toolchain %q (clojure)", tc)
	}
	if err := needBin(t.Rule, "clojure"); err != nil {
		return nil, err
	}
	stamp := marker(t, "clojure")
	argv := "sh -c 'cd " + orDot(t.Dir) + " && clojure -T:build uber'"
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

// ---------- scripting / data runtimes ----------

func recipePythonPkg(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		if b, ok := firstBin("uv", "pip", "poetry"); ok {
			tc = b
		} else if hasPythonModule("build") {
			tc = "build" // python3 -m build, module present
		} else if _, err := exec.LookPath("python3"); err == nil {
			return nil, fmt.Errorf("ebuild: python_package found python3 without the 'build' module; `pip install build`, install uv|pip|poetry, or set toolchain: explicitly")
		} else {
			return nil, fmt.Errorf("ebuild: python_package needs one of uv|pip|poetry|python3 (none on PATH); install one or set toolchain: explicitly")
		}
	}
	stamp := marker(t, "python")
	var argv string
	switch tc {
	case "uv":
		argv = "uv build --wheel --out-dir dist ."
	case "pip":
		argv = "pip wheel --no-deps --wheel-dir dist ."
	case "poetry":
		argv = "poetry build -f wheel -o dist"
	case "build":
		argv = "python3 -m build --wheel --outdir dist ."
	default:
		return nil, fmt.Errorf("ebuild: unknown Python toolchain %q (uv|pip|poetry|build)", tc)
	}
	return []Action{act(t, ctx.workdir, "python", stamp, argv, nil)}, nil
}

func recipePythonTest(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		if _, err := exec.LookPath("pytest"); err == nil {
			tc = "pytest"
		} else if _, err := exec.LookPath("python3"); err == nil {
			tc = "unittest" // stdlib fallback: python3 -m unittest
		} else {
			return nil, fmt.Errorf("ebuild: python_test needs one of pytest|python3 (none on PATH); install one or set toolchain: explicitly")
		}
	}
	paths := "tests"
	if len(t.Srcs) > 0 {
		paths = strings.Join(t.Srcs, " ")
	}
	stamp := marker(t, "pytest")
	var argv string
	switch tc {
	case "pytest":
		argv = "python3 -m pytest -q " + paths
	case "unittest":
		argv = "python3 -m unittest discover -s " + paths + " -t ."
	default:
		return nil, fmt.Errorf("ebuild: unknown Python test toolchain %q (pytest|unittest)", tc)
	}
	return []Action{act(t, ctx.workdir, "python", stamp, argv, nil)}, nil
}

func recipeNode(t Target, ctx recipeCtx, lib bool) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "esbuild"
	}
	if err := needBin(t.Rule, tc); err != nil {
		return nil, err
	}
	out := TargetOutput(t)
	switch tc {
	case "esbuild":
		entry, err := entryOf(t)
		if err != nil {
			return nil, err
		}
		argv := "esbuild " + entry + " --bundle --outfile=" + out
		return []Action{act(t, ctx.workdir, "node", out, argv, nil)}, nil
	case "tsc":
		entry, err := entryOf(t)
		if err != nil {
			return nil, err
		}
		if lib {
			argv := "tsc " + entry + " --declaration --emitDeclarationOnly --outDir dist"
			return []Action{act(t, ctx.workdir, "tsc", marker(t, "tsc"), argv, nil)}, nil
		}
		argv := "tsc " + entry + " --outDir .ebuild/tsc"
		stamp := marker(t, "tsc")
		return []Action{act(t, ctx.workdir, "tsc", stamp, argv, nil)}, nil
	case "webpack":
		cfg := t.Entry
		if cfg == "" {
			if len(t.Srcs) > 0 {
				cfg = t.Srcs[0]
			} else {
				cfg = "webpack.config.js"
			}
		}
		argv := "webpack --config " + cfg + " --mode production"
		stamp := marker(t, "webpack")
		return []Action{act(t, ctx.workdir, "node", stamp, argv, nil)}, nil
	case "vite":
		argv := "vite build"
		stamp := marker(t, "vite")
		return []Action{act(t, ctx.workdir, "node", stamp, argv, nil)}, nil
	}
	return nil, fmt.Errorf("ebuild: unknown JS/TS toolchain %q (esbuild|tsc|webpack|vite)", tc)
}

func recipeRuby(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "gem"
	}
	if tc != "gem" && tc != "bundler" {
		return nil, fmt.Errorf("ebuild: unknown Ruby toolchain %q (gem|bundler)", t.Toolchain)
	}
	if err := needBin(t.Rule, tc); err != nil {
		return nil, err
	}
	stamp := marker(t, "ruby")
	if tc == "bundler" {
		return []Action{act(t, ctx.workdir, "gen",
			stamp, "bundle install", nil)}, nil
	}
	var spec string
	for _, s := range t.Srcs {
		if strings.HasSuffix(s, ".gemspec") {
			spec = s
			break
		}
	}
	if spec == "" {
		return nil, fmt.Errorf("ebuild: ruby_gem needs a .gemspec in srcs")
	}
	return []Action{act(t, ctx.workdir, "gen",
		stamp, "gem build "+spec, nil)}, nil
}

func recipePHP(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc != "" && tc != "composer" {
		return nil, fmt.Errorf("ebuild: unknown PHP toolchain %q (composer)", tc)
	}
	if err := needBin(t.Rule, "composer"); err != nil {
		return nil, err
	}
	stamp := marker(t, "php")
	argv := "composer install --no-interaction --prefer-dist --no-dev --optimize-autoloader --working-dir=" + orDot(t.Dir)
	return []Action{act(t, ctx.workdir, "php", stamp, argv, nil)}, nil
}

func recipeJulia(t Target, ctx recipeCtx) ([]Action, error) {
	script := t.Entry
	if script == "" {
		if len(t.Srcs) == 0 {
			return nil, fmt.Errorf("ebuild: julia_script needs srcs or entry")
		}
		script = t.Srcs[0]
	}
	if err := needBin(t.Rule, "julia"); err != nil {
		return nil, err
	}
	stamp := marker(t, "julia")
	return []Action{act(t, ctx.workdir, "gen",
		stamp, "julia "+script, nil)}, nil
}

func recipeR(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needBin(t.Rule, "R"); err != nil {
		return nil, err
	}
	stamp := marker(t, "rlang")
	dir := orDot(t.Dir)
	argv := "sh -c 'cd " + dir + " && R CMD check --no-manual . && R CMD INSTALL .'"
	return []Action{act(t, ctx.workdir, "rlang", stamp, argv, nil)}, nil
}

func recipeLatex(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		var ok bool
		if tc, ok = firstBin("pdflatex", "xelatex", "lualatex", "latexmk"); !ok {
			return nil, fmt.Errorf("ebuild: latex_document needs one of pdflatex|xelatex|lualatex|latexmk (none on PATH); install one or set toolchain: explicitly")
		}
	}
	main := t.Entry
	if main == "" {
		for _, s := range t.Srcs {
			if strings.HasSuffix(s, ".tex") {
				main = s
				break
			}
		}
	}
	if main == "" {
		return nil, fmt.Errorf("ebuild: latex_document needs a .tex main (srcs or entry)")
	}
	out := t.Out
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(main), ".tex")
		out = "dist/" + base + ".pdf"
	}
	var argv string
	switch tc {
	case "pdflatex", "xelatex", "lualatex":
		argv = tc + " -interaction=nonstopmode -output-directory=dist " + main
	case "latexmk":
		argv = "latexmk -pdf -interaction=nonstopmode -output-directory=dist " + main
	default:
		return nil, fmt.Errorf("ebuild: unknown LaTeX toolchain %q (pdflatex|xelatex|lualatex|latexmk)", tc)
	}
	return []Action{act(t, ctx.workdir, "gen", out, argv, nil)}, nil
}

func recipeShell(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		tc = "sh"
	}
	switch tc {
	case "sh", "bash", "dash", "zsh":
	default:
		return nil, fmt.Errorf("ebuild: unknown shell toolchain %q (sh|bash|dash|zsh)", tc)
	}
	script := t.Entry
	if script == "" {
		if len(t.Srcs) == 0 {
			return nil, fmt.Errorf("ebuild: shell_script needs srcs or entry")
		}
		script = t.Srcs[0]
	}
	if err := needBin(t.Rule, tc); err != nil {
		return nil, err
	}
	stamp := marker(t, "sh")
	return []Action{act(t, ctx.workdir, "gen",
		stamp, tc+" "+script, nil)}, nil
}

// ---------- single-file compiled (zig/nim/crystal) ----------

func recipeZig(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	tc := t.Toolchain
	if tc != "" && tc != "zig" {
		return nil, fmt.Errorf("ebuild: unknown Zig toolchain %q (zig)", tc)
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, "zig"); err != nil {
		return nil, err
	}
	root := t.Entry
	if root == "" {
		root = t.Srcs[0]
	}
	argv := "zig build-exe " + root + " -O ReleaseSafe -femit-bin=" + out
	return []Action{act(t, ctx.workdir, "gen", out, argv, nil)}, nil
}

func recipeNim(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	tc := t.Toolchain
	if tc != "" && tc != "nim" {
		return nil, fmt.Errorf("ebuild: unknown Nim toolchain %q (nim)", tc)
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, "nim"); err != nil {
		return nil, err
	}
	return []Action{act(t, ctx.workdir, "gen",
		out, "nim c -d:release --out:"+out+" "+t.Srcs[0], nil)}, nil
}
func recipeCrystal(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	tc := t.Toolchain
	if tc != "" && tc != "crystal" {
		return nil, fmt.Errorf("ebuild: unknown Crystal toolchain %q (crystal)", tc)
	}
	out := TargetOutput(t)
	if err := needBin(t.Rule, "crystal"); err != nil {
		return nil, err
	}
	return []Action{act(t, ctx.workdir, "gen",
		out, "crystal build "+t.Srcs[0]+" -o "+out+" --release", nil)}, nil
}

// ---------- IDL / codegen ----------

func recipeProtoc(t Target, ctx recipeCtx, grpc bool) ([]Action, error) {
	if err := needBin(t.Rule, "protoc"); err != nil {
		return nil, err
	}
	if err := needSrcs(t, t.Name); err != nil {
		return nil, err
	}
	if len(t.Languages) == 0 {
		return nil, fmt.Errorf("ebuild: rule %s needs languages", t.Rule)
	}
	outdir := ".ebuild/proto/" + shortOf(t)
	if grpc {
		outdir = ".ebuild/grpc/" + shortOf(t)
	}
	goOut := map[string]string{"go": "--go_out", "python": "--python_out", "java": "--java_out", "cpp": "--cpp_out"}
	grpcOut := map[string]string{"go": "--go-grpc_out", "python": "--grpc_python_out", "java": "--grpc-java_out"}
	var flags []string
	for _, l := range t.Languages {
		if grpc {
			f, ok := grpcOut[l]
			if !ok {
				return nil, fmt.Errorf("ebuild: grpc unsupported language %q", l)
			}
			flags = append(flags, f+"="+outdir)
		} else {
			f, ok := goOut[l]
			if !ok {
				return nil, fmt.Errorf("ebuild: protobuf unsupported language %q", l)
			}
			flags = append(flags, f+"="+outdir)
		}
	}
	stamp := outdir + "/.gen-ok"
	argv := "protoc " + strings.Join(flags, " ") + " " + strings.Join(t.Srcs, " ")
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeOpenAPI(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needBin(t.Rule, "openapi-generator-cli"); err != nil {
		return nil, err
	}
	if t.Generator == "" {
		return nil, fmt.Errorf("ebuild: openapi_codegen needs generator (e.g. go, python)")
	}
	spec := t.Entry
	if spec == "" {
		if len(t.Srcs) == 0 {
			return nil, fmt.Errorf("ebuild: openapi_codegen needs a spec (srcs or entry)")
		}
		spec = t.Srcs[0]
	}
	outdir := ".ebuild/openapi/" + shortOf(t)
	stamp := outdir + "/.openapi-ok"
	argv := "openapi-generator-cli generate -i " + spec + " -g " + t.Generator + " -o " + outdir
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeGraphQL(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needBin(t.Rule, "graphql-codegen"); err != nil {
		return nil, err
	}
	stamp := marker(t, "graphql")
	argv := "graphql-codegen --config " + orDefault(t.Entry, "codegen.ts")
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

// ---------- containers / infra / frontend ----------

func recipeDocker(t Target, ctx recipeCtx) ([]Action, error) {
	engine := t.Toolchain
	if engine == "" {
		var ok bool
		if engine, ok = firstBin("docker", "podman"); !ok {
			return nil, fmt.Errorf("ebuild: docker_image needs one of docker|podman (none on PATH); install one or set toolchain: explicitly")
		}
	} else if engine != "docker" && engine != "podman" {
		return nil, fmt.Errorf("ebuild: unknown container toolchain %q (docker|podman)", engine)
	}
	tags := t.Tags
	if len(tags) == 0 {
		tags = []string{"ebuild/" + strings.ToLower(shortOf(t)) + ":latest"}
	}
	var tf []string
	for _, tag := range tags {
		tf = append(tf, "-t "+tag)
	}
	df := t.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	context := t.Context
	if context == "" {
		context = "."
	}
	iid := ".ebuild/docker/" + shortOf(t) + ".iid"
	argv := engine + " build -f " + df + " " + strings.Join(tf, " ") + " --iidfile " + iid + " " + context
	return []Action{act(t, ctx.workdir, "gen", iid, argv, nil)}, nil
}

func recipeHelm(t Target, ctx recipeCtx) ([]Action, error) {
	chart := orDot(t.Dir)
	stamp := marker(t, "helm")
	argv := "helm lint " + chart + " && helm package " + chart + " --destination dist"
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeTerraform(t Target, ctx recipeCtx) ([]Action, error) {
	dir := t.Dir
	if dir == "" {
		dir = t.Context
	}
	dir = orDot(dir)
	stamp := marker(t, "terraform")
	argv := "terraform -chdir=" + dir + " init -input=false && " +
		"terraform -chdir=" + dir + " validate && " +
		"terraform -chdir=" + dir + " apply -input=false -auto-approve"
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeAnsible(t Target, ctx recipeCtx, lint bool) ([]Action, error) {
	paths := t.Srcs
	if len(paths) == 0 {
		paths = []string{"."}
	}
	stamp := marker(t, "ansible")
	var argv string
	if lint {
		argv = "ansible-lint " + strings.Join(paths, " ")
	} else {
		argv = "ansible-playbook " + strings.Join(paths, " ")
	}
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

func recipeTailwind(t Target, ctx recipeCtx) ([]Action, error) {
	if err := needBin(t.Rule, "tailwindcss"); err != nil {
		return nil, err
	}
	in, err := entryOf(t)
	if err != nil {
		return nil, err
	}
	out := t.Out
	if out == "" {
		out = "dist/" + shortOf(t) + ".css"
	}
	return []Action{act(t, ctx.workdir, "gen",
		out, "tailwindcss -i "+in+" -o "+out+" --minify", nil)}, nil
}

func recipeSecurity(t Target, ctx recipeCtx) ([]Action, error) {
	tc := t.Toolchain
	if tc == "" {
		var ok bool
		if tc, ok = firstBin("trivy", "grype", "semgrep"); !ok {
			return nil, fmt.Errorf("ebuild: security_scan needs one of trivy|grype|semgrep (none on PATH); install one or set toolchain: explicitly")
		}
	}
	target := "."
	if len(t.Srcs) > 0 {
		target = t.Srcs[0]
	}
	stamp := marker(t, "security")
	var argv string
	switch tc {
	case "trivy":
		argv = "trivy fs --severity HIGH,CRITICAL --exit-code 1 " + target
	case "grype":
		argv = "grype " + target + " --fail-on high"
	case "semgrep":
		argv = "semgrep --config auto --error " + target
	default:
		return nil, fmt.Errorf("ebuild: unknown security toolchain %q (trivy|grype|semgrep)", tc)
	}
	return []Action{act(t, ctx.workdir, "gen", stamp, argv, nil)}, nil
}

// ---------- misc ----------

func recipeCustom(t Target, ctx recipeCtx) ([]Action, error) {
	if strings.TrimSpace(t.Cmd) == "" {
		return nil, fmt.Errorf("ebuild: custom_script target %s needs cmd", t.Name)
	}
	return []Action{act(t, ctx.workdir, "gen", t.Name, t.Cmd, nil)}, nil
}

// ---------- dispatch ----------

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// hasPythonModule reports whether python3 can import the module
// (e.g. "build", "pytest", "setuptools") without side effects.
func hasPythonModule(mod string) bool {
	py, err := exec.LookPath("python3")
	if err != nil {
		return false
	}
	cmd := exec.Command(py, "-c", "import "+mod)
	cmd.Env = HermeticEnv(nil, nil)
	return cmd.Run() == nil
}

// firstBin returns the first candidate found on PATH.
func firstBin(candidates ...string) (string, bool) {
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c, true
		}
	}
	return "", false
}

// resolveToolchain picks the toolchain: explicit choice is honored strictly
// (fail fast with the valid set); empty means "first available backend",
// so plans work on machines missing the preferred tool.
func resolveToolchain(t Target, backends []string) (string, error) {
	if t.Toolchain != "" {
		for _, b := range backends {
			if t.Toolchain == b {
				return b, nil
			}
		}
		return "", fmt.Errorf("ebuild: unknown %s toolchain %q (%s)", t.Rule, t.Toolchain, strings.Join(backends, "|"))
	}
	if b, ok := firstBin(backends...); ok {
		return b, nil
	}
	return "", fmt.Errorf("ebuild: %s needs one of %s (none on PATH); install one or set toolchain: explicitly",
		t.Rule, strings.Join(backends, "|"))
}

// needBin fails fast with an install hint when the resolved tool binary
// is not on PATH (instead of a cryptic fork/exec failure at run time).
func needBin(rule, bin string) error {
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("ebuild: rule %s needs %q on PATH; install it or set another toolchain:", rule, bin)
	}
	return nil
}

// ruleActions expands one target. libs maps library target names to their
// built artifacts for cross-target linking; hdrs maps them to headers for
// -I inference.
func ruleActions(t Target, workdir string, libs map[string]string, hdrs map[string][]string) ([]Action, error) {
	if !isValidRule(t.Rule) {
		return nil, fmt.Errorf("ebuild: unknown rule %q for target %s (valid: %s)",
			t.Rule, t.Name, strings.Join(validRules, ", "))
	}
	// Single-toolchain rules: validate an explicit override and probe the
	// binary now, so missing tools fail here with an install hint.
	if tools := ToolchainsForRule(t.Rule); len(tools) == 1 {
		if t.Toolchain != "" && t.Toolchain != tools[0] {
			return nil, fmt.Errorf("ebuild: rule %s only supports toolchain %q", t.Rule, tools[0])
		}
		if err := needBin(t.Rule, tools[0]); err != nil {
			return nil, err
		}
	}
	ctx := recipeCtx{workdir: workdir, libs: libs, hdrs: hdrs}
	switch t.Rule {
	case "cc_binary":
		return recipeCC(t, ctx, false)
	case "cxx_binary":
		return recipeCC(t, ctx, true)
	case "cc_library":
		return recipeCLib(t, ctx, false)
	case "cxx_library":
		return recipeCLib(t, ctx, true)
	case "rust_binary":
		return recipeRust(t, ctx, false)
	case "rust_library":
		return recipeRust(t, ctx, true)
	case "go_binary":
		return recipeGo(t, ctx, false)
	case "go_library":
		return recipeGo(t, ctx, true)
	case "python_package":
		return recipePythonPkg(t, ctx)
	case "python_test":
		return recipePythonTest(t, ctx)
	case "typescript_app":
		return recipeNode(t, ctx, false)
	case "typescript_library":
		return recipeNode(t, ctx, true)
	case "java_binary":
		return recipeJava(t, ctx, false)
	case "java_library":
		return recipeJava(t, ctx, true)
	case "kotlin_app":
		return recipeKotlin(t, ctx, false)
	case "kotlin_library":
		return recipeKotlin(t, ctx, true)
	case "swift_binary":
		return recipeSwift(t, ctx, false)
	case "swift_library":
		return recipeSwift(t, ctx, true)
	case "scala_app":
		return recipeScala(t, ctx, false)
	case "scala_library":
		return recipeScala(t, ctx, true)
	case "protobuf_library":
		return recipeProtoc(t, ctx, false)
	case "grpc_service":
		return recipeProtoc(t, ctx, true)
	case "docker_image":
		return recipeDocker(t, ctx)
	case "helm_chart":
		return recipeHelm(t, ctx)
	case "terraform_apply":
		return recipeTerraform(t, ctx)
	case "ansible_playbook":
		return recipeAnsible(t, ctx, false)
	case "ansible_lint":
		return recipeAnsible(t, ctx, true)
	case "shell_script":
		return recipeShell(t, ctx)
	case "ruby_gem":
		return recipeRuby(t, ctx)
	case "php_composer":
		return recipePHP(t, ctx)
	case "csharp_assembly", "fsharp_assembly":
		return recipeDotnet(t, ctx)
	case "clojure_project":
		return recipeClojure(t, ctx)
	case "haskell_package":
		return recipeHaskell(t, ctx)
	case "ocaml_executable":
		return recipeOCaml(t, ctx)
	case "zig_binary":
		return recipeZig(t, ctx)
	case "nim_binary":
		return recipeNim(t, ctx)
	case "crystal_app":
		return recipeCrystal(t, ctx)
	case "elixir_mix":
		return recipeElixir(t, ctx)
	case "erlang_otp":
		return recipeErlang(t, ctx)
	case "julia_script":
		return recipeJulia(t, ctx)
	case "r_package":
		return recipeR(t, ctx)
	case "latex_document":
		return recipeLatex(t, ctx)
	case "openapi_codegen":
		return recipeOpenAPI(t, ctx)
	case "graphql_schema":
		return recipeGraphQL(t, ctx)
	case "tailwind_css":
		return recipeTailwind(t, ctx)
	case "webpack_bundle":
		return recipeNode(t, ctx, false)
	case "vite_build":
		return recipeVite(t, ctx)
	case "security_scan":
		return recipeSecurity(t, ctx)
	case "custom_script":
		return recipeCustom(t, ctx)
	}
	return nil, fmt.Errorf("ebuild: unimplemented rule %q", t.Rule)
}

func recipeVite(t Target, ctx recipeCtx) ([]Action, error) {
	stamp := marker(t, "vite")
	argv := "vite build"
	if t.Entry != "" {
		argv += " --config " + t.Entry
	}
	return []Action{act(t, ctx.workdir, "node", stamp, argv, nil)}, nil
}
