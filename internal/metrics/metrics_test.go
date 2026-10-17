package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounterAndRegistry(t *testing.T) {
	r := &Registry{counters: map[string]*Counter{}}
	r.Inc("a")
	r.Inc("a")
	r.Counter("b").Add(5)
	snap := r.Snapshot()
	if snap["a"] != 2 || snap["b"] != 5 {
		t.Fatalf("snap %v", snap)
	}
	if r.Counter("a").Get() != 2 {
		t.Fatal("counter get")
	}
	// same instance returned
	if r.Counter("a") != r.Counter("a") {
		t.Fatal("not same counter")
	}
}

func TestHealthEndpoints(t *testing.T) {
	// use fresh registry via Default
	Default.Inc("test_metric")
	h := Handler()
	cases := []struct {
		path string
		want string
		ct   string
	}{
		{"/healthz", `"status":"ok"`, "application/json"},
		{"/readyz", `"status":"ready"`, "application/json"},
		{"/metrics", "test_metric", "text/plain"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		resp := w.Result()
		body := w.Body.String()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d", c.path, resp.StatusCode)
		}
		if !strings.Contains(body, c.want) {
			t.Fatalf("%s body %q missing %q", c.path, body, c.want)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, c.ct) {
			t.Fatalf("%s ct %q want %q", c.path, ct, c.ct)
		}
	}
}

func TestMetricsEmpty(t *testing.T) {
	r := &Registry{counters: map[string]*Counter{}}
	// Snapshot empty
	if len(r.Snapshot()) != 0 {
		t.Fatal("expected empty")
	}
}
