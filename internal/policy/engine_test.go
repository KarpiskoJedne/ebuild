package policy

import (
	"testing"

	"github.com/ebuild/ebuild/internal/core"
)

func TestEngineCheck(t *testing.T) {
	e := &Engine{Rules: []Rule{{
		Name:    "no-system-toolchain",
		DenyIf:  "toolchain==system",
		Message: "system toolchains forbidden",
	}}}
	cfg := &core.Config{Targets: []core.Target{{
		Name:      "//a:b",
		Rule:      "cc_binary",
		Toolchain: "system",
	}}}
	violations := e.Check(cfg)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].Target != "//a:b" {
		t.Fatalf("expected //a:b, got %s", violations[0].Target)
	}
}

func TestEngineNoViolation(t *testing.T) {
	e := &Engine{Rules: []Rule{{
		Name:   "no-system-toolchain",
		DenyIf: "toolchain==system",
	}}}
	cfg := &core.Config{Targets: []core.Target{{
		Name:      "//a:b",
		Rule:      "cc_binary",
		Toolchain: "official",
	}}}
	violations := e.Check(cfg)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(violations))
	}
}

func TestViolatedExpressions(t *testing.T) {
	target := core.Target{Name: "//x", Rule: "go_binary"}

	if !violated(target, "rule==go_binary") {
		t.Fatal("expected rule== match")
	}
	if violated(target, "rule==rust_binary") {
		t.Fatal("expected rule== no match")
	}
	if !violated(target, "rule!=rust_binary") {
		t.Fatal("expected rule!= match")
	}
	if violated(target, "rule!=go_binary") {
		t.Fatal("expected rule!= no match")
	}
	if !violated(target, "rule~go_.*") {
		t.Fatal("expected rule~ match")
	}
	if violated(target, "rule~rust_.*") {
		t.Fatal("expected rule~ no match")
	}
	if !violated(target, "tag:missing:release") {
		t.Fatal("expected tag:missing match")
	}
	target.Tags = []string{"release"}
	if violated(target, "tag:missing:release") {
		t.Fatal("expected tag:missing no match")
	}
	if violated(target, "") {
		t.Fatal("expected empty expr no match")
	}
}
