// Migration engine: Bazel, CMake, Make, and Python projects → ebuild.yml.
package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// From names a supported source layout.
type From string

const (
	FromBazel  From = "bazel"
	FromCMake  From = "cmake"
	FromMake   From = "make"
	FromPython From = "python"
)

// Migrate converts the project at root from the given layout and returns
// the generated ebuild.yml document.
func Migrate(from From, root string) (string, error) {
	var targets []Target
	var err error
	switch from {
	case FromBazel:
		targets, err = migrateBazel(root)
	case FromCMake:
		targets, err = migrateCMake(root)
	case FromMake:
		targets, err = migrateMake(root)
	case FromPython:
		targets, err = migratePython(root)
	default:
		return "", fmt.Errorf("ebuild: unsupported --from=%q (bazel|cmake|make|python)", from)
	}
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("ebuild: no targets discovered under %s", root)
	}
	return RenderYAML("migrated", targets), nil
}

// RenderYAML serializes targets into an ebuild.yml document (schema v1).
func RenderYAML(project string, targets []Target) string {
	var b strings.Builder
	b.WriteString("version: 1\n\nproject:\n")
	fmt.Fprintf(&b, "  name: %s\n  root: .\n\ntargets:\n", project)
	for _, t := range targets {
		fmt.Fprintf(&b, "  - name: %s\n", t.Name)
		fmt.Fprintf(&b, "    rule: %s\n", t.Rule)
		if len(t.Srcs) > 0 {
			fmt.Fprintf(&b, "    srcs: [%s]\n", quoteList(t.Srcs))
		}
		if len(t.Hdrs) > 0 {
			fmt.Fprintf(&b, "    hdrs: [%s]\n", quoteList(t.Hdrs))
		}
		if len(t.Deps) > 0 {
			fmt.Fprintf(&b, "    deps: [%s]\n", quoteList(t.Deps))
		}
		if t.Out != "" {
			fmt.Fprintf(&b, "    out: %s\n", t.Out)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func quoteList(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

var bazelRuleRe = regexp.MustCompile(`^(cc_binary|cc_library|go_binary|go_library|py_binary|py_library|java_binary)\s*\(\s*$`)
var bazelAttrRe = regexp.MustCompile(`^\s*(name|srcs|hdrs|deps)\s*=\s*(.*)$`)

func migrateBazel(root string) ([]Target, error) {
	ruleFor := map[string]string{
		"cc_binary": "cc_binary", "cc_library": "cc_library",
		"java_binary": "java_binary", "go_binary": "go_binary",
		"go_library": "go_library", "py_binary": "python_binary",
		"py_library": "python_package",
	}
	var targets []Target
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "BUILD" && !strings.HasSuffix(d.Name(), ".bazel") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		var cur *Target
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if m := bazelRuleRe.FindStringSubmatch(line); m != nil {
				rule, ok := ruleFor[m[1]]
				if !ok {
					rule = "custom_script"
				}
				targets = append(targets, Target{Rule: rule})
				cur = &targets[len(targets)-1]
				continue
			}
			if cur == nil {
				continue
			}
			if m := bazelAttrRe.FindStringSubmatch(line); m != nil {
				vals := bazelStringList(m[2])
				switch m[1] {
				case "name":
					if len(vals) > 0 {
						rel, _ := filepath.Rel(root, filepath.Dir(p))
						cur.Name = "//" + filepath.ToSlash(rel) + ":" + vals[0]
					}
				case "srcs":
					cur.Srcs = vals
				case "hdrs":
					cur.Hdrs = vals
				case "deps":
					cur.Deps = vals
				}
			}
			if line == ")" {
				cur = nil
			}
		}
		return nil
	})
	return targets, err
}

func bazelStringList(s string) []string {
	re := regexp.MustCompile(`"([^"]+)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.TrimPrefix(m[1], ":"))
	}
	return out
}

var cmakeExeRe = regexp.MustCompile(`^add_executable\s*\(\s*(\S+)\s*(.*)\)\s*$`)
var cmakeLibRe = regexp.MustCompile(`^add_library\s*\(\s*(\S+)\s*(.*)\)\s*$`)

func migrateCMake(root string) ([]Target, error) {
	var targets []Target
	f, err := os.Open(filepath.Join(root, "CMakeLists.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := cmakeExeRe.FindStringSubmatch(line); m != nil {
			targets = append(targets, Target{
				Name: "//:" + m[1], Rule: "cc_binary", Srcs: strings.Fields(m[2]),
			})
		} else if m := cmakeLibRe.FindStringSubmatch(line); m != nil {
			targets = append(targets, Target{
				Name: "//:" + m[1], Rule: "cc_library", Srcs: strings.Fields(m[2]),
			})
		}
	}
	return targets, sc.Err()
}

var makeTargetRe = regexp.MustCompile(`^([A-Za-z0-9_./-]+)\s*:(.*)$`)

func migrateMake(root string) ([]Target, error) {
	seen := map[string]Target{}
	order := []string{}
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		f, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") && strings.Contains(line, "=") {
				continue
			}
			if m := makeTargetRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				name := m[1]
				if name == ".PHONY" || strings.HasSuffix(name, ".o") {
					continue
				}
				t := seen[name]
				t.Name = "//:" + strings.ReplaceAll(name, "/", "_")
				t.Rule = "custom_script"
				for _, d := range strings.Fields(m[2]) {
					if strings.HasSuffix(d, ".c") || strings.HasSuffix(d, ".cc") || strings.HasSuffix(d, ".cpp") {
						t.Srcs = append(t.Srcs, d)
					} else {
						t.Deps = append(t.Deps, "//:"+strings.ReplaceAll(d, "/", "_"))
					}
				}
				if _, ok := seen[name]; !ok {
					order = append(order, name)
				}
				seen[name] = t
			}
		}
		f.Close()
	}
	var out []Target
	for _, n := range order {
		out = append(out, seen[n])
	}
	return out, nil
}

func migratePython(root string) ([]Target, error) {
	var targets []Target
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	pkg := filepath.Base(root)
	switch {
	case names["pyproject.toml"] || names["setup.py"] || names["setup.cfg"]:
		targets = append(targets, Target{Name: "//:" + pkg, Rule: "python_package", Srcs: collectPy(root)})
	case names["requirements.txt"]:
		targets = append(targets, Target{Name: "//:" + pkg, Rule: "python_package", Srcs: collectPy(root)})
	}
	sort.Strings([]string{}) // keep sort import live across edits
	return targets, nil
}

func collectPy(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".py") {
			if rel, err := filepath.Rel(root, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}
