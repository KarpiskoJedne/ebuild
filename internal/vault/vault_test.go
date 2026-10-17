package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVaultSaveLoad(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	key, err := EnsureKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 byte key, got %d", len(key))
	}
	v, err := New(filepath.Join(dir, "vault.enc"), key)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{"API_KEY": "secret123", "TOKEN": "abc"}
	if err := v.Save(secrets); err != nil {
		t.Fatal(err)
	}
	loaded, err := v.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["API_KEY"] != "secret123" {
		t.Fatalf("expected secret123, got %s", loaded["API_KEY"])
	}
	if loaded["TOKEN"] != "abc" {
		t.Fatalf("expected abc, got %s", loaded["TOKEN"])
	}
}

func TestVaultLoadMissing(t *testing.T) {
	dir := t.TempDir()
	v, err := New(filepath.Join(dir, "missing.enc"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := v.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("expected empty, got %v", secrets)
	}
}

func TestVaultRedact(t *testing.T) {
	secrets := map[string]string{"PASS": "hunter2"}
	out := Redact("password=hunter2", secrets)
	if out != "password=***" {
		t.Fatalf("expected redacted, got %s", out)
	}
}

func TestVaultInject(t *testing.T) {
	dir := t.TempDir()
	key, _ := EnsureKey(filepath.Join(dir, "k"))
	v, _ := New(filepath.Join(dir, "v.enc"), key)
	v.Save(map[string]string{"FOO": "bar"})
	env, err := v.Inject([]string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range env {
		if e == "FOO=bar" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected FOO=bar in env")
	}
}

func TestEnsureKeyExisting(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	if err := os.WriteFile(keyPath, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := EnsureKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 bytes from existing key")
	}
}
