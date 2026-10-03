// LSP integration: stdio JSON-RPC client (Content-Length framing) with
// symbol→target dependency mapping for precise auto-discovery.
package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Symbol is a workspace/symbol response entry mapped onto build targets.
type Symbol struct {
	Name   string
	Kind   int
	File   string
	Line   int
	Target string // owning ebuild target, resolved via srcs index
}

// Client manages one language-server subprocess over stdio pipes.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	pending map[int64]chan json.RawMessage
	nextID  int64
	msgs    []json.RawMessage // server→client notifications (drained by Serve)
}

// Start launches serverCmd (e.g. clangd, rust-analyzer, gopls) with args.
func Start(serverCmd string, args ...string) (*Client, error) {
	cmd := exec.Command(serverCmd, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, pending: map[int64]chan json.RawMessage{}}
	go c.serve(stdout)
	return c, nil
}

func (c *Client) serve(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		hdr, err := readHeaders(br)
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(hdr["Content-Length"])
		if n <= 0 || n > 64<<20 {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		var base struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &base); err != nil {
			continue
		}
		if base.ID != nil {
			c.mu.Lock()
			ch := c.pending[*base.ID]
			delete(c.pending, *base.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- base.Result
			}
		} else {
			c.mu.Lock()
			c.msgs = append(c.msgs, body)
			c.mu.Unlock()
		}
	}
}

func readHeaders(br *bufio.Reader) (map[string]string, error) {
	out := map[string]string{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return out, nil
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
}

// Call sends a request and waits for the raw result.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	var pv json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		pv = b
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(pv),
	})
	c.mu.Lock()
	_, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	res, ok := <-ch
	if !ok {
		return nil, fmt.Errorf("ebuild: lsp %s: server closed", method)
	}
	return res, nil
}

// Notify sends a fire-and-forget notification (e.g. textDocument/didOpen).
func (c *Client) Notify(method string, params any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// Initialize performs the LSP handshake for rootURI.
func (c *Client) Initialize(rootURI string) error {
	_, err := c.Call("initialize", map[string]any{
		"processId": nil, "rootUri": rootURI,
		"capabilities": map[string]any{},
	})
	if err != nil {
		return err
	}
	return c.Notify("initialized", map[string]any{})
}

// WorkspaceSymbols queries workspace/symbol and maps files onto targets.
func (c *Client) WorkspaceSymbols(targets []Target, query string) ([]Symbol, error) {
	res, err := c.Call("workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name     string `json:"name"`
		Kind     int    `json:"kind"`
		Location struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"location"`
	}
	if err := json.Unmarshal(res, &raw); err != nil {
		return nil, err
	}
	owner := map[string]string{} // file -> target
	for i := range targets {
		for _, s := range targets[i].Srcs {
			owner[s] = targets[i].Name
		}
	}
	var out []Symbol
	for _, r := range raw {
		file := strings.TrimPrefix(r.Location.URI, "file://")
		out = append(out, Symbol{
			Name: r.Name, Kind: r.Kind, File: file,
			Line: r.Location.Range.Start.Line, Target: owner[file],
		})
	}
	return out, nil
}

// Shutdown terminates the server gracefully.
func (c *Client) Shutdown() error {
	_, _ = c.Call("shutdown", nil)
	_ = c.Notify("exit", nil)
	return c.cmd.Wait()
}
