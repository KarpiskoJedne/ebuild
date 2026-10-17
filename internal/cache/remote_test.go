package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteHas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	r := NewRemote(srv.URL, "")
	ok, err := r.Has(context.Background(), HashBytes([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected Has true")
	}
}

func TestRemoteHas404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	r := NewRemote(srv.URL, "")
	ok, err := r.Has(context.Background(), HashBytes([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected Has false")
	}
}

func TestRemoteDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("blob"))
	}))
	defer srv.Close()
	r := NewRemote(srv.URL, "")
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	hash := HashBytes([]byte("blob"))
	if err := r.Download(context.Background(), hash, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "blob" {
		t.Fatalf("expected blob, got %q", string(data))
	}
}

func TestRemoteUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	r := NewRemote(srv.URL, "")
	err := r.Upload(context.Background(), HashBytes([]byte("data")), []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
}
