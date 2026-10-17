package history

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func tempPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func TestDashboardHandler(t *testing.T) {
	s := New(tempPath(t, "history.jsonl"))
	h := DashboardHandler(s)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ebuild dashboard") {
		t.Fatal("expected dashboard html")
	}
}

func TestDashboardAPIHistory(t *testing.T) {
	s := New(tempPath(t, "history.jsonl"))
	s.Append(Record{Target: "//a:b", Rule: "go_binary", Success: true})
	h := DashboardHandler(s)
	req := httptest.NewRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "//a:b") {
		t.Fatal("expected target in history json")
	}
}

func TestDashboardAPIFlaky(t *testing.T) {
	s := New(tempPath(t, "history.jsonl"))
	s.Append(Record{Target: "//x:y", Success: true})
	s.Append(Record{Target: "//x:y", Success: false})
	h := DashboardHandler(s)
	req := httptest.NewRequest("GET", "/api/flaky", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "//x:y") {
		t.Fatal("expected flaky target in json")
	}
}
