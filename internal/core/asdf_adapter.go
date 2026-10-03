// asdf version-manager integration: parse .tool-versions and ebuild.yml
// pins, resolve installed runtimes, and delegate installs to the asdf CLI.
package core

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Pin is one tool@version requirement.
type Pin struct {
	Tool    string
	Version string
}

// pluginFor maps a toolchain binary name to its asdf plugin name. Only
// well-established community plugins are listed; anything else (cc, c++,
// javac wrappers, tsc, ...) falls back to free-text versions — the adapter
// never guesses a plugin name.
var pluginFor = map[string]string{
	"go":      "golang",
	"cargo":   "rust",
	"rustc":   "rust",
	"python":  "python",
	"python3": "python",
	"node":    "nodejs",
	"nodejs":  "nodejs",
	"java":    "java",
	"kotlin":  "kotlin",
	"kotlinc": "kotlin",
	"lua":     "lua",
	"php":     "php",
	"perl":    "perl",
	"r":       "r",
	"R":       "r",
}

// PluginForTool returns the asdf plugin managing the given toolchain binary.
func PluginForTool(tool string) (string, bool) {
	p, ok := pluginFor[tool]
	return p, ok
}

// HasCLI reports whether the asdf executable is on PATH.
func HasCLI() bool {
	_, err := exec.LookPath("asdf")
	return err == nil
}

// HasPlugin reports whether asdf knows the plugin (`asdf plugin list`).
func HasPlugin(plugin string) bool {
	asdf, err := exec.LookPath("asdf")
	if err != nil {
		return false
	}
	out, err := exec.Command(asdf, "plugin", "list").CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == plugin {
			return true
		}
	}
	return false
}

// ListInstalled returns versions of plugin already installed via asdf
// (`asdf list <plugin>` prints one version per line), sorted newest-first
// by numeric segments (1.24.0 > 1.23.10 > 1.9).
func ListInstalled(plugin string) ([]string, error) {
	asdf, err := exec.LookPath("asdf")
	if err != nil {
		return nil, fmt.Errorf("ebuild: asdf CLI not found")
	}
	if !HasPlugin(plugin) {
		return nil, fmt.Errorf("ebuild: asdf plugin %q not installed (run `asdf plugin add %s`)", plugin, plugin)
	}
	out, err := exec.Command(asdf, "list", plugin).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ebuild: asdf list %s: %w", plugin, err)
	}
	var versions []string
	for _, line := range strings.Split(string(out), "\n") {
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		v = strings.TrimSpace(v)
		if v == "" || strings.Contains(v, " ") || strings.HasPrefix(v, "No ") {
			continue
		}
		versions = append(versions, v)
	}
	SortVersions(versions)
	return versions, nil
}

// SortVersions orders version strings newest-first, comparing leading
// numeric dot-segments (1.24.0 > 1.23.10) and falling back to string
// order for the remainder (21.0.2+9 > 21.0.2+7, "system" last).
func SortVersions(versions []string) {
	numParts := func(v string) ([]int, string) {
		digits := ""
		i := 0
		for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '.') {
			digits += string(v[i])
			i++
		}
		var parts []int
		for _, p := range strings.Split(digits, ".") {
			n, _ := strconv.Atoi(p)
			parts = append(parts, n)
		}
		return parts, v[i:]
	}
	sort.SliceStable(versions, func(i, j int) bool {
		pi, ri := numParts(versions[i])
		pj, rj := numParts(versions[j])
		for k := 0; k < len(pi) && k < len(pj); k++ {
			if pi[k] != pj[k] {
				return pi[k] > pj[k]
			}
		}
		if len(pi) != len(pj) {
			return len(pi) > len(pj)
		}
		if ri != rj {
			return ri > rj
		}
		return versions[i] > versions[j]
	})
}

// ParseToolVersions reads .tool-versions ("<tool> <version>" per line,
// '#' comments) into pins.
func ParseToolVersions(path string) ([]Pin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pins []Pin
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pins = append(pins, Pin{Tool: fields[0], Version: fields[1]})
	}
	return pins, sc.Err()
}

// PinsFromConfig converts ebuild.yml toolchains into pins.
func PinsFromConfig(cfg *Config) []Pin {
	var pins []Pin
	for tool, ver := range cfg.Toolchains {
		pins = append(pins, Pin{Tool: tool, Version: ver})
	}
	return pins
}

// AsdfRoot locates the asdf installation (ASDF_DIR or ~/.asdf).
func AsdfRoot() string {
	if d := os.Getenv("ASDF_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".asdf")
}

// InstallPath returns the directory asdf would install tool@version into.
func InstallPath(tool, version string) string {
	return filepath.Join(AsdfRoot(), "installs", tool, version)
}

// IsInstalled reports whether tool@version has a bin directory on disk.
func IsInstalled(tool, version string) bool {
	bin := filepath.Join(InstallPath(tool, version), "bin")
	fi, err := os.Stat(bin)
	return err == nil && fi.IsDir()
}

// Ensure guarantees tool@version is available: if already installed it
// returns its bin dir; otherwise it shells out to `asdf install <plugin>
// <version>` (the only supported provisioner) and re-checks. The asdf
// plugin name is resolved via PluginForTool, falling back to the tool name.
func Ensure(pin Pin) (string, error) {
	plugin, _ := PluginForTool(pin.Tool)
	if plugin == "" {
		plugin = pin.Tool
	}
	if IsInstalled(plugin, pin.Version) {
		return filepath.Join(InstallPath(plugin, pin.Version), "bin"), nil
	}
	return InstallVersion(plugin, pin.Version)
}

// InstallVersion runs `asdf install plugin version` and returns the bin dir.
func InstallVersion(plugin, version string) (string, error) {
	asdf, err := exec.LookPath("asdf")
	if err != nil {
		return "", fmt.Errorf("ebuild: %s@%s not installed and asdf CLI not found", plugin, version)
	}
	cmd := exec.Command(asdf, "install", plugin, version)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ebuild: asdf install %s %s: %w", plugin, version, err)
	}
	if !IsInstalled(plugin, version) {
		return "", fmt.Errorf("ebuild: asdf reported success but %s not found", InstallPath(plugin, version))
	}
	return filepath.Join(InstallPath(plugin, version), "bin"), nil
}

// ShimsDir returns the asdf shims directory for PATH assembly.
func ShimsDir() string {
	return filepath.Join(AsdfRoot(), "shims")
}
