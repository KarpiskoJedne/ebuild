// Package daemon — zero-CGO bridge to the native ebuild daemon.
//
// Wire protocol (shared with worker_host.rs): length-prefixed frames over
// Unix Domain Sockets (Linux/macOS) or Named Pipes (Windows).
// Frame = 4-byte big-endian length + JSON body:
// request:  {"id":1,"method":"status|watch|submit|poll|hash","params":{...}}
// response: {"id":1,"result":{...},"error":""}
package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSocketPath returns the platform IPC endpoint.
func DefaultSocketPath() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\ebuild-daemon`
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "ebuild.sock")
	}
	return filepath.Join(os.TempDir(), "ebuild.sock")
}

// Event is a file/task event delivered by the daemon.
type Event struct {
	Kind    uint32 `json:"kind"`
	Cookie  uint32 `json:"cookie"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    uint64 `json:"size"`
	MtimeNs uint64 `json:"mtime_ns"`
}

// Task is a build task submitted to the daemon.
type Task struct {
	ID   uint64 `json:"id"`
	Lang uint32 `json:"lang"`
	Op   uint32 `json:"op"`
	Path string `json:"path"`
	Args string `json:"args"`
}

type request struct {
	ID     uint64         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Client is a concurrency-safe IPC connection.
type Client struct {
	conn   net.Conn
	mu     sync.Mutex
	nextID uint64
}

// Dial connects to the daemon endpoint.
func Dial(endpoint string) (*Client, error) {
	conn, err := dialEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) call(method string, params map[string]any) (json.RawMessage, error) {
	id := atomic.AddUint64(&c.nextID, 1)
	body, _ := json.Marshal(request{ID: id, Method: method, Params: params})
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(append(hdr[:], body...)); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 64<<20 {
		return nil, fmt.Errorf("ebuild: ipc: bad frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return nil, err
	}
	var resp response
	if err := json.Unmarshal(buf, &resp); err != nil {
		return nil, err
	}
	if resp.ID != id {
		return nil, fmt.Errorf("ebuild: ipc: id mismatch")
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("ebuild: daemon: %s", resp.Error)
	}
	return resp.Result, nil
}

// Status returns the daemon status string.
func (c *Client) Status() (string, error) {
	raw, err := c.call("status", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// Watch asks the daemon to watch path recursively.
func (c *Client) Watch(path string, recursive bool) error {
	_, err := c.call("watch", map[string]any{"path": path, "recursive": recursive})
	return err
}

// SubmitTask enqueues a build task, returning the assigned ID.
func (c *Client) SubmitTask(t Task) (uint64, error) {
	raw, err := c.call("submit", map[string]any{
		"id": t.ID, "lang": t.Lang, "op": t.Op, "path": t.Path, "args": t.Args,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// PollEvent waits up to timeout for one event; timeout<=0 polls once.
func (c *Client) PollEvent(timeout time.Duration) (Event, error) {
	ms := int64(timeout / time.Millisecond)
	raw, err := c.call("poll", map[string]any{"timeout_ms": ms})
	if err != nil {
		return Event{}, err
	}
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// HashFile asks the daemon to content-hash a path via mmap.
func (c *Client) HashFile(path string) (string, error) {
	raw, err := c.call("hash", map[string]any{"path": path})
	if err != nil {
		return "", err
	}
	var out struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Hash, nil
}

// Close terminates the connection.
func (c *Client) Close() error { return c.conn.Close() }
