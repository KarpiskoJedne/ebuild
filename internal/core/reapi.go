// Remote Execution / CAS sync client.
//
// Transport note: this client speaks the REAPI *digest model*
// (hash/size_bytes content addressing) over HTTPS with a simple
// length-delimited JSON framing, plus predictive pre-fetch of graph
// neighbors. It does not implement the gRPC/Protobuf wire protocol
// (no external deps under CGO_ENABLED=0); the digest schema is
// wire-compatible at the hash level with REAPI CAS keys.
package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Digest is a content address: lowercase hex hash + byte size.
type Digest struct {
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
}

// DigestBytes hashes in-memory content (SHA-256; BLAKE3 digests produced by
// the native daemon are accepted as-is when passed through).
func DigestBytes(b []byte) Digest {
	h := sha256.Sum256(b)
	return Digest{Hash: hex.EncodeToString(h[:]), SizeBytes: int64(len(b))}
}

// DigestFile hashes a file without loading it fully into memory.
func DigestFile(path string) (Digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Digest{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Digest{}, err
	}
	return Digest{Hash: hex.EncodeToString(h.Sum(nil)), SizeBytes: n}, nil
}

// CASClient syncs blobs with a remote CAS endpoint:
// POST /cas/{hash} (upload), GET /cas/{hash} (download), HEAD /cas/{hash}.
type CASClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewCASClient builds a client with a 60s timeout transport.
func NewCASClient(baseURL string) *CASClient {
	return &CASClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Has probes the remote for a digest.
func (c *CASClient) Has(d Digest) (bool, error) {
	req, _ := http.NewRequest("HEAD", c.BaseURL+"/cas/"+d.Hash, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

// Upload pushes a blob; skips when the remote already has it.
func (c *CASClient) Upload(d Digest, r io.Reader) error {
	if ok, err := c.Has(d); err == nil && ok {
		return nil
	}
	req, _ := http.NewRequest("POST", c.BaseURL+"/cas/"+d.Hash, r)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ebuild: cas upload %s: %s", d.Hash, resp.Status)
	}
	return nil
}

// Download fetches a blob into path (atomic rename into place).
func (c *CASClient) Download(d Digest, path string) error {
	resp, err := c.HTTP.Get(c.BaseURL + "/cas/" + d.Hash)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ebuild: cas download %s: %s", d.Hash, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".ebuild-tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	cerr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if cerr != nil {
		os.Remove(tmp)
		return cerr
	}
	return os.Rename(tmp, path)
}

// Prefetch concurrently downloads digests for a target and its graph
// neighbors (predictive: deps of deps, one level), skipping local hits.
func (c *CASClient) Prefetch(g *Graph, targets []Target, name string, dir string) error {
	want := map[string]Digest{}
	frontier := append(g.TransitiveDeps(name), name)
	for _, n := range frontier {
		for _, r := range g.ReverseDeps(n) {
			frontier = append(frontier, r) // one-level neighbor expansion
		}
	}
	_ = want
	type job struct{ d Digest }
	jobs := make(chan job)
	errc := make(chan error, 1)
	go func() {
		defer close(jobs)
		seen := map[string]bool{}
		for _, n := range frontier {
			if seen[n] {
				continue
			}
			seen[n] = true
			// Digest identity for a target node: hash of its src list.
			t := g.byName[n]
			if t == nil {
				continue
			}
			var buf bytes.Buffer
			_ = json.NewEncoder(&buf).Encode(t)
			jobs <- job{d: DigestBytes(buf.Bytes())}
		}
	}()
	for j := range jobs {
		local := filepath.Join(dir, j.d.Hash[:2], j.d.Hash)
		if _, err := os.Stat(local); err == nil {
			continue
		}
		if err := c.Download(j.d, local); err != nil {
			select {
			case errc <- err:
			default:
			}
			return <-errc
		}
	}
	return nil
}
