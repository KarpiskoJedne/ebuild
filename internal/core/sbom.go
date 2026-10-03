// SBOM generation (CycloneDX JSON, SPDX tag-value) and SLSA provenance
// attestation wrapping Sigstore/cosign when available on PATH.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Component is one SBOM entry: a built target or source input.
type Component struct {
	Name    string
	Version string
	Hash    string
	Kind    string // "target" or "source"
}

// CollectComponents derives SBOM components from the target closure.
func CollectComponents(targets []Target) []Component {
	seen := map[string]Component{}
	for _, t := range targets {
		seen[t.Name] = Component{Name: t.Name, Version: "0.0.0", Kind: "target"}
		for _, s := range append(append([]string{}, t.Srcs...), t.Hdrs...) {
			if b, err := os.ReadFile(s); err == nil {
				h := sha256.Sum256(b)
				seen[s] = Component{Name: s, Kind: "source", Hash: hex.EncodeToString(h[:])}
			} else {
				seen[s] = Component{Name: s, Kind: "source"}
			}
		}
	}
	out := make([]Component, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CycloneDX renders components as CycloneDX 1.5 JSON.
func CycloneDX(project string, comps []Component) string {
	type hash struct {
		Alg string `json:"alg"`
		Val string `json:"content"`
	}
	type comp struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Version string `json:"version"`
		Hashes  []hash `json:"hashes,omitempty"`
	}
	doc := map[string]any{
		"bomFormat":   "CycloneDX",
		"specVersion": "1.5",
		"metadata":    map[string]any{"component": map[string]string{"name": project}},
	}
	var list []comp
	for _, c := range comps {
		cc := comp{Type: "application", Name: c.Name, Version: c.Version}
		if c.Kind == "source" {
			cc.Type = "file"
		}
		if c.Hash != "" {
			cc.Hashes = []hash{{Alg: "SHA-256", Val: c.Hash}}
		}
		list = append(list, cc)
	}
	doc["components"] = list
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b)
}

// SPDX renders components as SPDX 2.3 tag-value.
func SPDX(project string, comps []Component) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SPDXVersion: SPDX-2.3\nDataLicense: CC0-1.0\nSPDXID: SPDXRef-DOCUMENT\n")
	fmt.Fprintf(&b, "DocumentName: %s\nDocumentNamespace: https://ebuild.dev/sbom/%s\n", project, project)
	for i, c := range comps {
		fmt.Fprintf(&b, "\nPackageName: %s\nSPDXID: SPDXRef-Package-%d\n", c.Name, i+1)
		fmt.Fprintf(&b, "PackageDownloadLocation: NOASSERTION\nFilesAnalyzed: false\n")
		if c.Hash != "" {
			fmt.Fprintf(&b, "PackageChecksum: SHA256: %s\n", c.Hash)
		}
		fmt.Fprintf(&b, "PackageLicenseConcluded: NOASSERTION\nPackageLicenseDeclared: NOASSERTION\n")
	}
	return b.String()
}

// Provenance is a SLSA v1 predicate describing one built artifact.
type Provenance struct {
	Builder   string   `json:"builder"`
	BuildType string   `json:"buildType"`
	Target    string   `json:"target"`
	Rule      string   `json:"rule"`
	Inputs    []string `json:"inputs"`
	Digest    string   `json:"digest"`
	BuiltAt   string   `json:"builtAt"`
}

// Attest builds a provenance predicate for target+digest.
func Attest(t Target, digest string) Provenance {
	var inputs []string
	inputs = append(inputs, t.Srcs...)
	inputs = append(inputs, t.Hdrs...)
	inputs = append(inputs, t.Deps...)
	return Provenance{
		Builder: "https://ebuild.dev/daemon@v1", BuildType: "https://ebuild.dev/lua-rule@v1",
		Target: t.Name, Rule: t.Rule, Inputs: inputs,
		Digest: digest, BuiltAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// SignWithCosign shells out to cosign/sigstore to sign blobPath; it returns
// the signature bundle path. Requires cosign on PATH.
func SignWithCosign(blobPath string) (string, error) {
	cosign, err := exec.LookPath("cosign")
	if err != nil {
		return "", fmt.Errorf("ebuild: cosign not on PATH")
	}
	sigPath := blobPath + ".sig"
	cmd := exec.Command(cosign, "sign-blob", "--yes", "--output-signature", sigPath, blobPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ebuild: cosign sign-blob: %w", err)
	}
	return sigPath, nil
}
