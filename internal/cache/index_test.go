package cache

import (
	"path/filepath"
	"testing"
)

func TestHashBytes(t *testing.T) {
	h := HashBytes([]byte("hello"))
	if len(h) != 64 {
		t.Fatalf("expected 64 char hash, got %d", len(h))
	}
	if h != HashBytes([]byte("hello")) {
		t.Fatal("hash not deterministic")
	}
	if h == HashBytes([]byte("world")) {
		t.Fatal("different inputs should hash differently")
	}
}

func TestIndexStoreHas(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{Root: dir}
	hash := HashBytes([]byte("hello"))
	if err := idx.Store(hash, []byte("world")); err != nil {
		t.Fatal(err)
	}
	if !idx.Has(hash) {
		t.Fatal("expected Has to be true after Store")
	}
	if idx.Has(HashBytes([]byte("missing"))) {
		t.Fatal("expected Has false for unknown hash")
	}
}

func TestIndexPathSharding(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{Root: dir}
	hash := HashBytes([]byte("test"))
	expected := filepath.Join(dir, hash[:2], hash)
	if p := idx.Path(hash); p != expected {
		t.Fatalf("expected %s, got %s", expected, p)
	}
}

func TestIndexGC(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{Root: dir}
	for i := 0; i < 5; i++ {
		hash := HashBytes([]byte{byte(i)})
		if err := idx.Store(hash, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := idx.GC(0); err != nil {
		t.Fatal(err)
	} else if removed != 5 {
		t.Fatalf("expected 5 removed, got %d", removed)
	}
	for i := 0; i < 5; i++ {
		hash := HashBytes([]byte{byte(i)})
		if idx.Has(hash) {
			t.Fatalf("expected %s removed", hash)
		}
	}
}

func TestValidHash(t *testing.T) {
	good := HashBytes([]byte("data"))
	if err := ValidHash(good); err != nil {
		t.Fatalf("expected valid hash, got %v", err)
	}
	if err := ValidHash("short"); err == nil {
		t.Fatal("expected error for short hash")
	}
	if err := ValidHash("gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"); err == nil {
		t.Fatal("expected error for non-hex hash")
	}
}
