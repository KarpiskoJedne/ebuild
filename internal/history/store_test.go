package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAppendAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	s := New(path)
	rec := Record{Target: "//a:b", Rule: "go_binary", DurationMs: 100, Success: true}
	if err := s.Append(rec); err != nil {
		t.Fatal(err)
	}
	recs, err := s.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Target != "//a:b" {
		t.Fatalf("expected //a:b, got %s", recs[0].Target)
	}
	if recs[0].Time.IsZero() {
		t.Fatal("expected time set")
	}
}

func TestStoreFlaky(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	s := New(path)
	now := time.Now()
	s.Append(Record{Target: "//x:y", Success: true, Time: now})
	s.Append(Record{Target: "//x:y", Success: false, Time: now})
	s.Append(Record{Target: "//x:z", Success: true, Time: now})
	f, err := s.Flaky()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f["//x:y"]; !ok {
		t.Fatal("expected //x:y flaky")
	}
	if _, ok := f["//x:z"]; ok {
		t.Fatal("expected //x:z not flaky")
	}
	if f["//x:y"][0] != 1 || f["//x:y"][1] != 1 {
		t.Fatalf("expected 1 success 1 fail, got %+v", f["//x:y"])
	}
}

func TestStoreListMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	s := New(path)
	for i := 0; i < 5; i++ {
		s.Append(Record{Target: "//t", Success: true})
	}
	recs, err := s.List(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2, got %d", len(recs))
	}
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath("/tmp/root")
	expected := "/tmp/root/.ebuild/history/history.jsonl"
	if p != expected {
		t.Fatalf("expected %s, got %s", expected, p)
	}
}

func TestStoreListMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	s := New(path)
	recs, err := s.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if recs != nil {
		t.Fatal("expected nil for missing file")
	}
}
