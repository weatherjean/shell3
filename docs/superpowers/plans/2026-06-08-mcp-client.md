# MCP Client Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let shell3 connect to external MCP servers over stdio, advertise their tools to the LLM (prefixed `server__tool`), and dispatch tool calls to them — enabling e.g. `chrome-devtools-mcp` browser control.

**Architecture:** A new stdlib-only `internal/mcp` package provides a JSON-RPC-over-stdio `Client` and a `Manager` that does discover-and-cache schema loading, lazy per-server sessions, prefix mapping, and dispatch. `internal/luacfg` gains a `shell3.mcp{}` registration and an agent `tools.mcp` selector mirroring the existing custom-tool pattern. `internal/agentsetup` builds one Manager from all declared servers and wires its tool definitions + a dedicated `MCPTool`/`MCPToolNames` dispatch seam into `chat.Config`. MCP calls pass through the existing `on_tool_call` guard chain unchanged.

**Tech Stack:** Go 1.25 (stdlib `os/exec`, `encoding/json`, `bufio`, `context`, `syscall`), gopher-lua (existing), module `github.com/weatherjean/shell3`.

**Spec:** `docs/superpowers/specs/2026-06-08-mcp-support-design.md`

---

## File Structure

- `internal/mcp/protocol.go` (new) — JSON-RPC message types, public `ToolSchema`/`Result`/`Spec` types.
- `internal/mcp/client.go` (new) — stdio `Client`: spawn, handshake, list, call, close.
- `internal/mcp/client_test.go` (new) — client tests driven by an in-test fake server (re-exec trick).
- `internal/mcp/manager.go` (new) — `Manager`: discover-and-cache, lazy sessions, prefix mapping, dispatch.
- `internal/mcp/manager_test.go` (new) — manager tests against a fake client.
- `internal/luacfg/luacfg.go` (modify) — `MCPServer` struct, `LoadedConfig.MCPServers` map, `Agent.MCPServerNames`.
- `internal/luacfg/register.go` (modify) — `shell3.mcp` registration, `luaMCP`, `mcp` gate key, `tools.mcp` parse.
- `internal/luacfg/register_test.go` (modify/new) — parse tests.
- `internal/chat/chat.go` (modify) — `MCPTool`/`MCPToolNames` on `Config` + `ActiveAgent` + `ApplyActiveAgent`.
- `internal/chat/turn.go` (modify) — dispatch branch for MCP-owned names.
- `internal/agentsetup/agentsetup.go` (modify) — build Manager, merge tool defs, set seam, shutdown.
- `internal/scaffold/defaults/shell3.lua` (modify) — commented example MCP server.

---

## Task 1: MCP protocol types and public API types

**Files:**
- Create: `internal/mcp/protocol.go`
- Test: `internal/mcp/protocol_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/mcp/protocol_test.go
package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseToolsListResult(t *testing.T) {
	raw := json.RawMessage(`{"tools":[
		{"name":"navigate_page","description":"Go to a URL","inputSchema":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}}
	]}`)
	got, err := parseToolsList(raw)
	if err != nil {
		t.Fatalf("parseToolsList: %v", err)
	}
	if len(got) != 1 || got[0].Name != "navigate_page" {
		t.Fatalf("unexpected tools: %+v", got)
	}
	if got[0].InputSchema["type"] != "object" {
		t.Fatalf("inputSchema not preserved: %+v", got[0].InputSchema)
	}
}

func TestParseCallResultFlattensText(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"isError":false}`)
	res, err := parseCallResult(raw)
	if err != nil {
		t.Fatalf("parseCallResult: %v", err)
	}
	if res.Text != "hello world" || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestParseCallResultUsesStructuredWhenNoContent(t *testing.T) {
	raw := json.RawMessage(`{"content":[],"structuredContent":{"pages":2},"isError":true}`)
	res, err := parseCallResult(raw)
	if err != nil {
		t.Fatalf("parseCallResult: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError true")
	}
	if res.Text != `{"pages":2}` {
		t.Fatalf("expected structured JSON, got %q", res.Text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestParse -v`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/mcp/protocol.go
package mcp

import (
	"encoding/json"
	"strings"
)

// Spec describes a declared MCP server (stdio transport only).
type Spec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string // optional allowlist; empty means all tools
}

// ToolSchema is one tool exposed by an MCP server.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema
}

// Result is a flattened tools/call result.
type Result struct {
	Text    string
	IsError bool
}

// JSON-RPC 2.0 wire types (internal).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func parseToolsList(raw json.RawMessage) ([]ToolSchema, error) {
	var payload struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := make([]ToolSchema, 0, len(payload.Tools))
	for _, t := range payload.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, ToolSchema{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

func parseCallResult(raw json.RawMessage) (Result, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Result{}, err
	}
	var b strings.Builder
	for _, c := range payload.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	text := b.String()
	if text == "" && len(payload.StructuredContent) > 0 {
		text = string(payload.StructuredContent)
	}
	return Result{Text: text, IsError: payload.IsError}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestParse -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/protocol.go internal/mcp/protocol_test.go
git commit -m "feat(mcp): JSON-RPC types and result parsing"
```

---

## Task 2: stdio Client with handshake, list, call, close

The client spawns the server, performs the `initialize` handshake, and exposes `ListTools`/`CallTool`. Tests use a **fake MCP server** implemented in the same test binary via a re-exec trick (no separate build, no Chrome).

**Files:**
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/client_test.go`

- [ ] **Step 1: Write the failing test (with the in-binary fake server)**

```go
// internal/mcp/client_test.go
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain doubles as a fake MCP server when MCP_FAKE=1 is set. The client
// under test spawns os.Args[0] with that env, so no external binary is needed.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE") == "1" {
		runFakeServer()
		return
	}
	os.Exit(m.Run())
}

// runFakeServer speaks newline-delimited JSON-RPC: initialize, tools/list,
// tools/call (echoes its args), and ignores notifications.
func runFakeServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var req rpcResponse // reuse fields; ID + Method via a loose decode
		var probe struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(line, &req)
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Method == "" || (probe.Method != "initialize" && probe.Method != "tools/list" && probe.Method != "tools/call") {
			continue // notification or unknown — no reply
		}
		var result string
		switch probe.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fake","version":"0"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"echo args","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}}}}]}`
		case "tools/call":
			var p struct {
				Name      string                 `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(probe.Params, &p)
			msg, _ := p.Arguments["msg"].(string)
			result = fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"isError":false}`, "echo:"+msg)
		}
		fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", probe.ID, result)
	}
}

func fakeSpec() Spec {
	return Spec{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=TestMain"}, Env: map[string]string{"MCP_FAKE": "1"}}
}

func TestClientListAndCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := New(fakeSpec())
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "echo:hi" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestClient -v`
Expected: FAIL — `New`, `Start`, `ListTools`, `CallTool`, `Close` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/mcp/client.go
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const stderrCap = 16 * 1024

// Client is a JSON-RPC-over-stdio MCP client for a single server process.
type Client struct {
	spec Spec

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse
	closed  bool

	stderrMu  sync.Mutex
	stderrBuf []byte
}

// New builds a client for spec; it does not start the process.
func New(spec Spec) *Client {
	return &Client{spec: spec, pending: map[int]chan rpcResponse{}}
}

// Start spawns the server and performs the initialize handshake.
func (c *Client) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	cmd := exec.CommandContext(runCtx, c.spec.Command, c.spec.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group for clean kill
	if len(c.spec.Env) > 0 {
		env := append([]string{}, cmdEnviron()...)
		for k, v := range c.spec.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("mcp %q: start: %w", c.spec.Name, err)
	}
	c.cmd = cmd
	c.stdin = stdin
	go c.readLoop(stdout)
	go c.drainStderr(stderr)

	// Handshake: initialize -> initialized notification.
	initParams := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "shell3", "version": "0"},
	}
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		c.Close()
		return fmt.Errorf("mcp %q: initialize: %w (stderr: %s)", c.spec.Name, err, c.stderrTail())
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		c.Close()
		return err
	}
	return nil
}

// ListTools returns the server's advertised tools.
func (c *Client) ListTools(ctx context.Context) ([]ToolSchema, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseToolsList(raw)
}

// CallTool invokes a tool and returns its flattened result.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return Result{}, err
	}
	return parseCallResult(raw)
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %q: client closed", c.spec.Name)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.stdin == nil {
		return fmt.Errorf("mcp %q: client closed", c.spec.Name)
	}
	_, err = c.stdin.Write(b)
	return err
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var resp rpcResponse
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue // notification from server; ignored in MVP
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *Client) drainStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			c.stderrBuf = append(c.stderrBuf, buf[:n]...)
			if len(c.stderrBuf) > stderrCap {
				c.stderrBuf = c.stderrBuf[len(c.stderrBuf)-stderrCap:]
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) stderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return string(c.stderrBuf)
}

// Close stops the server: close stdin, cancel context, kill the process group.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	stdin := c.stdin
	cmd := c.cmd
	c.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	if cmd != nil && cmd.Process != nil {
		// Kill the whole process group (npx -> node -> chrome).
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}
```

```go
// internal/mcp/environ.go
package mcp

import "os"

// cmdEnviron is a seam so tests can keep the parent environment.
func cmdEnviron() []string { return os.Environ() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestClient -v`
Expected: PASS.

- [ ] **Step 5: Run the whole package and vet**

Run: `go test ./internal/mcp/ && go vet ./internal/mcp/`
Expected: PASS, no vet complaints.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/client.go internal/mcp/environ.go internal/mcp/client_test.go
git commit -m "feat(mcp): stdio client with handshake, list, call, group-kill close"
```

---

## Task 3: Manager — discover-and-cache, lazy sessions, prefix dispatch

**Files:**
- Create: `internal/mcp/manager.go`
- Create: `internal/mcp/manager_test.go`

The manager maps prefixed names (`fake__echo`), loads schemas from cache or a one-shot probe, lazily opens a long-lived session per server on first call, and flattens results to strings. A `newClient` field makes the client injectable so tests avoid real processes.

- [ ] **Step 1: Write the failing test**

```go
// internal/mcp/manager_test.go
package mcp

import (
	"context"
	"path/filepath"
	"testing"
)

// fakeClient implements the clientAPI seam in-process.
type fakeClient struct {
	started   int
	listCalls int
	callCalls int
	closed    bool
}

func (f *fakeClient) Start(ctx context.Context) error { f.started++; return nil }
func (f *fakeClient) ListTools(ctx context.Context) ([]ToolSchema, error) {
	f.listCalls++
	return []ToolSchema{{Name: "echo", Description: "d", InputSchema: map[string]any{"type": "object"}}}, nil
}
func (f *fakeClient) CallTool(ctx context.Context, name string, args map[string]any) (Result, error) {
	f.callCalls++
	return Result{Text: "ok:" + name}, nil
}
func (f *fakeClient) Close() error { f.closed = true; return nil }

func newTestManager(t *testing.T, fc *fakeClient) *Manager {
	t.Helper()
	m := NewManager([]Spec{{Name: "fake", Command: "x"}}, t.TempDir())
	m.newClient = func(Spec) clientAPI { return fc }
	return m
}

func TestManagerDefsArePrefixed(t *testing.T) {
	fc := &fakeClient{}
	m := newTestManager(t, fc)
	defs, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"})
	if err != nil {
		t.Fatalf("ToolDefinitionsFor: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "fake__echo" {
		t.Fatalf("expected fake__echo, got %+v", defs)
	}
	// Discovery probe must have started and closed a throwaway client once.
	if fc.started != 1 || !fc.closed {
		t.Fatalf("probe lifecycle wrong: started=%d closed=%v", fc.started, fc.closed)
	}
}

func TestManagerDiscoveryUsesCacheSecondTime(t *testing.T) {
	dir := t.TempDir()
	fc1 := &fakeClient{}
	m1 := NewManager([]Spec{{Name: "fake", Command: "x"}}, dir)
	m1.newClient = func(Spec) clientAPI { return fc1 }
	if _, err := m1.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stat(filepath.Join(dir, "fake.tools.json")); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// New manager, same dir: must read cache, not probe.
	fc2 := &fakeClient{}
	m2 := NewManager([]Spec{{Name: "fake", Command: "x"}}, dir)
	m2.newClient = func(Spec) clientAPI { return fc2 }
	if _, err := m2.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	if fc2.started != 0 {
		t.Fatalf("expected cache hit (no probe), got started=%d", fc2.started)
	}
}

func TestManagerDispatchLazySessionReused(t *testing.T) {
	fc := &fakeClient{}
	m := newTestManager(t, fc)
	if _, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	startsAfterProbe := fc.started // 1 (probe). Probe client is closed.
	out, err := m.Dispatch(context.Background(), "fake__echo", `{"msg":"hi"}`)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out != "ok:echo" {
		t.Fatalf("unexpected: %q", out)
	}
	_, _ = m.Dispatch(context.Background(), "fake__echo", `{}`)
	// Session is created once and reused: exactly one extra Start beyond probe.
	if fc.started != startsAfterProbe+1 {
		t.Fatalf("session not reused: started=%d", fc.started)
	}
	if fc.callCalls != 2 {
		t.Fatalf("expected 2 calls, got %d", fc.callCalls)
	}
	m.Shutdown()
	if !fc.closed {
		t.Fatalf("Shutdown did not close session")
	}
}

func TestManagerAllowlistFilters(t *testing.T) {
	fc := &fakeClient{}
	m := NewManager([]Spec{{Name: "fake", Command: "x", Tools: []string{"nope"}}}, t.TempDir())
	m.newClient = func(Spec) clientAPI { return fc }
	defs, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 0 {
		t.Fatalf("allowlist should exclude echo, got %+v", defs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestManager -v`
Expected: FAIL — `NewManager`, `Manager`, `clientAPI`, `stat` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/mcp/manager.go
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/llm"
)

// clientAPI is the subset of *Client the manager needs (seam for tests).
type clientAPI interface {
	Start(ctx context.Context) error
	ListTools(ctx context.Context) ([]ToolSchema, error)
	CallTool(ctx context.Context, name string, args map[string]any) (Result, error)
	Close() error
}

// Manager owns declared servers: schema discovery+cache, lazy sessions, dispatch.
type Manager struct {
	specs    map[string]Spec
	cacheDir string

	newClient func(Spec) clientAPI

	mu       sync.Mutex
	schemas  map[string][]ToolSchema // server -> discovered (allowlist-filtered) tools
	sessions map[string]clientAPI    // server -> live session
}

// NewManager builds a manager for the given servers; cacheDir holds schema caches.
func NewManager(specs []Spec, cacheDir string) *Manager {
	m := &Manager{
		specs:     map[string]Spec{},
		cacheDir:  cacheDir,
		schemas:   map[string][]ToolSchema{},
		sessions:  map[string]clientAPI{},
		newClient: func(s Spec) clientAPI { return New(s) },
	}
	for _, s := range specs {
		m.specs[s.Name] = s
	}
	return m
}

// stat is a tiny seam used by tests.
func stat(p string) (os.FileInfo, error) { return os.Stat(p) }

func specHash(s Spec) string {
	h := sha256.New()
	h.Write([]byte(s.Command))
	for _, a := range s.Args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte{1})
		h.Write([]byte(k))
	}
	for _, t := range s.Tools {
		h.Write([]byte{2})
		h.Write([]byte(t))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type cacheFile struct {
	Hash  string       `json:"hash"`
	Tools []ToolSchema `json:"tools"`
}

// discover loads a server's schemas: cache hit if hash matches, else one-shot probe.
func (m *Manager) discover(ctx context.Context, name string) ([]ToolSchema, error) {
	if s, ok := m.schemas[name]; ok {
		return s, nil
	}
	spec, ok := m.specs[name]
	if !ok {
		return nil, fmt.Errorf("mcp: unknown server %q", name)
	}
	want := specHash(spec)
	cachePath := filepath.Join(m.cacheDir, name+".tools.json")

	if data, err := os.ReadFile(cachePath); err == nil {
		var cf cacheFile
		if json.Unmarshal(data, &cf) == nil && cf.Hash == want {
			m.schemas[name] = cf.Tools
			return cf.Tools, nil
		}
	}

	// Probe: spawn a throwaway client just to list tools, then close it.
	cl := m.newClient(spec)
	if err := cl.Start(ctx); err != nil {
		return nil, err
	}
	tools, err := cl.ListTools(ctx)
	_ = cl.Close()
	if err != nil {
		return nil, err
	}
	tools = filterTools(tools, spec.Tools)

	if err := os.MkdirAll(m.cacheDir, 0o755); err == nil {
		if b, err := json.Marshal(cacheFile{Hash: want, Tools: tools}); err == nil {
			_ = os.WriteFile(cachePath, b, 0o644)
		}
	}
	m.schemas[name] = tools
	return tools, nil
}

func filterTools(tools []ToolSchema, allow []string) []ToolSchema {
	if len(allow) == 0 {
		return tools
	}
	set := map[string]bool{}
	for _, a := range allow {
		set[a] = true
	}
	out := tools[:0:0]
	for _, t := range tools {
		if set[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// ToolDefinitionsFor returns prefixed llm.ToolDefinition for the named servers.
func (m *Manager) ToolDefinitionsFor(ctx context.Context, names []string) ([]llm.ToolDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var defs []llm.ToolDefinition
	for _, name := range names {
		tools, err := m.discover(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, t := range tools {
			defs = append(defs, llm.ToolDefinition{
				Name:        name + "__" + t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	return defs, nil
}

// ToolNamesFor reports the prefixed names owned by the named servers (best-effort:
// uses already-discovered schemas; call ToolDefinitionsFor first).
func (m *Manager) ToolNamesFor(names []string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, name := range names {
		for _, t := range m.schemas[name] {
			out[name+"__"+t.Name] = true
		}
	}
	return out
}

// Dispatch routes a prefixed tool call to its server's session (lazily created).
func (m *Manager) Dispatch(ctx context.Context, prefixedName, argsJSON string) (string, error) {
	server, tool, ok := strings.Cut(prefixedName, "__")
	if !ok {
		return "", fmt.Errorf("mcp: not a prefixed tool name: %q", prefixedName)
	}
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("mcp: bad args json: %w", err)
		}
	}

	sess, err := m.session(ctx, server)
	if err != nil {
		return "", err
	}
	res, err := sess.CallTool(ctx, tool, args)
	if err != nil {
		// Transport error: drop the poisoned session so the next call reconnects.
		m.mu.Lock()
		if m.sessions[server] == sess {
			delete(m.sessions, server)
		}
		m.mu.Unlock()
		_ = sess.Close()
		return "", err
	}
	if res.IsError {
		return "error: " + res.Text, nil
	}
	return res.Text, nil
}

func (m *Manager) session(ctx context.Context, server string) (clientAPI, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[server]; ok {
		return s, nil
	}
	spec, ok := m.specs[server]
	if !ok {
		return nil, fmt.Errorf("mcp: unknown server %q", server)
	}
	cl := m.newClient(spec)
	if err := cl.Start(ctx); err != nil {
		return nil, err
	}
	m.sessions[server] = cl
	return cl, nil
}

// Shutdown closes all live sessions.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]clientAPI{}
	m.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/ -v`
Expected: PASS (all protocol, client, manager tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/manager.go internal/mcp/manager_test.go
git commit -m "feat(mcp): manager with discover-and-cache, lazy sessions, prefix dispatch"
```

---

## Task 4: luacfg config surface — `shell3.mcp{}` and `tools.mcp`

**Files:**
- Modify: `internal/luacfg/luacfg.go` (add struct + maps)
- Modify: `internal/luacfg/register.go` (registration + parsing)
- Create: `internal/luacfg/mcp_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/luacfg/mcp_test.go
package luacfg

import (
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lua")
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	lc, err := Load(path, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(lc.Close)
	return lc
}

func TestMCPServerParsed(t *testing.T) {
	lc := writeConfig(t, `
shell3.model("m", { base_url="x", api_key="k", model="o", context_window=1000 })
local chrome = shell3.mcp({
  name = "chrome",
  command = "npx",
  args = { "-y", "chrome-devtools-mcp@latest" },
  tools = { "navigate_page" },
})
shell3.agent({ name="base", model="m", prompt="p",
  tools = { bash = true, mcp = { chrome } } })
`)
	srv, ok := lc.MCPServers["chrome"]
	if !ok {
		t.Fatalf("server not registered: %+v", lc.MCPServers)
	}
	if srv.Command != "npx" || len(srv.Args) != 2 || srv.Args[1] != "chrome-devtools-mcp@latest" {
		t.Fatalf("bad server: %+v", srv)
	}
	if len(srv.Tools) != 1 || srv.Tools[0] != "navigate_page" {
		t.Fatalf("bad allowlist: %+v", srv.Tools)
	}
	a := lc.Active()
	if len(a.MCPServerNames) != 1 || a.MCPServerNames[0] != "chrome" {
		t.Fatalf("agent did not select server: %+v", a.MCPServerNames)
	}
}

func TestMCPRequiresNameAndCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lua")
	_ = writeFile(path, `
shell3.model("m", { base_url="x", api_key="k", model="o", context_window=1000 })
shell3.mcp({ name = "bad" })
shell3.agent({ name="base", model="m", prompt="p", tools = { bash = true } })
`)
	if _, err := Load(path, dir); err == nil {
		t.Fatalf("expected error for missing command")
	}
}
```

```go
// internal/luacfg/testutil_test.go
package luacfg

import "os"

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
```

(If a `writeFile`/config helper already exists in this package's tests, reuse it and skip `testutil_test.go`. Check with: `grep -rn "func writeFile\|os.WriteFile" internal/luacfg/*_test.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestMCP -v`
Expected: FAIL — `MCPServers`, `MCPServerNames`, `shell3.mcp` undefined / nil map.

- [ ] **Step 3: Add the struct and maps**

In `internal/luacfg/luacfg.go`, add the struct after `CustomTool`:

```go
// MCPServer is a declared external MCP server (stdio transport).
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string // optional allowlist
}
```

Add a field to `Agent` (in the `Agent` struct):

```go
	MCPServerNames []string
```

Add a field to `LoadedConfig` (next to `Tools`):

```go
	MCPServers map[string]MCPServer
```

Initialize it where `Tools` is initialized (`internal/luacfg/luacfg.go:85`):

```go
	c := &LoadedConfig{Tools: map[string]CustomTool{}, MCPServers: map[string]MCPServer{}, Secrets: env, L: lua.NewState()}
```

- [ ] **Step 4: Register `shell3.mcp` and parse `tools.mcp`**

In `internal/luacfg/register.go` `registerShell3`, add after the `tool` registration:

```go
	L.SetField(tbl, "mcp", L.NewFunction(c.luaMCP))
```

Add the key allowlists near the other `*Keys` vars:

```go
var mcpKeys = map[string]bool{
	"name": true, "command": true, "args": true, "env": true, "tools": true,
}
```

Add `mcp` to `toolGateKeys`:

```go
var toolGateKeys = map[string]bool{
	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
	"history": true, "custom": true, "skill": true,
	"prune": true, "compact": true, "mcp": true,
}
```

Add the `luaMCP` function (mirrors `luaTool`):

```go
func (c *LoadedConfig) luaMCP(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "mcp", mcpKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	srv := MCPServer{Name: optStr(opts, "name"), Command: optStr(opts, "command")}
	if srv.Name == "" || srv.Command == "" {
		L.RaiseError("mcp: name and command are required")
	}
	if a, ok := opts.RawGetString("args").(*lua.LTable); ok {
		srv.Args = stringList(a)
	}
	if e, ok := opts.RawGetString("env").(*lua.LTable); ok {
		srv.Env = map[string]string{}
		e.ForEach(func(k, v lua.LValue) {
			if ks, ok := k.(lua.LString); ok {
				srv.Env[string(ks)] = v.String()
			}
		})
	}
	if tools, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		srv.Tools = stringList(tools)
	}
	c.MCPServers[srv.Name] = srv

	h := L.NewTable()
	h.RawSetString("__mcp", lua.LString(srv.Name))
	L.Push(h)
	return 1
}
```

In `luaAgent`, inside the `if tt, ok := opts.RawGetString("tools")...` block (right after the `custom` parse), add:

```go
		if mc, ok := tt.RawGetString("mcp").(*lua.LTable); ok {
			a.MCPServerNames = handleNames(mc, "__mcp")
		}
```

Add the `stringList` helper to `internal/luacfg/convert.go`:

```go
// stringList reads the array part of a Lua table as a []string.
func stringList(t *lua.LTable) []string {
	var out []string
	for i := 1; ; i++ {
		v := t.RawGetInt(i)
		if v == lua.LNil {
			break
		}
		out = append(out, v.String())
	}
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/luacfg/ -run TestMCP -v`
Expected: PASS (both tests).

- [ ] **Step 6: Run the whole luacfg package**

Run: `go test ./internal/luacfg/`
Expected: PASS (no regressions).

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): shell3.mcp{} server declaration and tools.mcp selector"
```

---

## Task 5: chat.Config MCP dispatch seam

**Files:**
- Modify: `internal/chat/chat.go`
- Modify: `internal/chat/turn.go`
- Modify: `internal/chat/toolhandler.go` (carry seam into TurnConfig)
- Test: `internal/chat/mcp_dispatch_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/mcp_dispatch_test.go
package chat

import (
	"context"
	"testing"
)

func TestDispatchRoutesMCPName(t *testing.T) {
	called := ""
	cfg := TurnConfig{
		MCPToolNames: map[string]bool{"chrome__navigate_page": true},
		MCPTool: func(ctx context.Context, name, args string) (string, error) {
			called = name
			return "navigated", nil
		},
	}
	out := dispatchMCPTool(context.Background(), cfg.MCPTool, "chrome__navigate_page", `{"url":"x"}`)
	if out != "navigated" || called != "chrome__navigate_page" {
		t.Fatalf("unexpected: out=%q called=%q", out, called)
	}
}

func TestDispatchMCPToolNilSeam(t *testing.T) {
	out := dispatchMCPTool(context.Background(), nil, "chrome__navigate_page", `{}`)
	if out == "" {
		t.Fatalf("expected an error string when seam is nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestDispatchMCP -v` and `-run TestDispatchRoutesMCP`
Expected: FAIL — `MCPToolNames`/`MCPTool` fields and `dispatchMCPTool` undefined.

- [ ] **Step 3: Add the seam to Config, ActiveAgent, ApplyActiveAgent, TurnConfig**

In `internal/chat/chat.go`, add to the `Config` struct (near `CustomTool`/`CustomToolNames`, ~line 94):

```go
	// MCPTool dispatches a prefixed MCP tool call (server__tool) by name.
	// Nil means no MCP servers are wired.
	MCPTool func(ctx context.Context, name, argsJSON string) (string, error)
	// MCPToolNames is the set of prefixed tool names routed to MCPTool.
	MCPToolNames map[string]bool
```

Add to the `ActiveAgent` struct (near `CustomToolNames`, ~line 34):

```go
	MCPToolNames map[string]bool
```

In `ApplyActiveAgent` (after `c.CustomToolNames = rt.CustomToolNames`, ~line 134):

```go
	c.MCPToolNames = rt.MCPToolNames
```

In the `TurnConfig` assembly (`internal/chat/chat.go` ~line 173, where `CustomTool`/`CustomToolNames` are copied into the turn config):

```go
		MCPTool:          cfg.MCPTool,
		MCPToolNames:     cfg.MCPToolNames,
```

In `internal/chat/toolhandler.go`, add to the `TurnConfig` struct (near its `CustomTool`/`CustomToolNames`, ~line 78):

```go
	// MCPTool dispatches a prefixed MCP tool call (server__tool) by name.
	MCPTool func(ctx context.Context, name, argsJSON string) (string, error)
	// MCPToolNames is the set of prefixed tool names routed to MCPTool.
	MCPToolNames map[string]bool
```

- [ ] **Step 4: Add the dispatch helper and routing branch**

In `internal/chat/tools.go`, add next to `dispatchCustomTool`:

```go
// dispatchMCPTool calls mcp for a prefixed MCP tool. If mcp is nil it returns an
// error string for the model.
func dispatchMCPTool(ctx context.Context, mcp func(ctx context.Context, name, argsJSON string) (string, error), name, rawArgs string) string {
	if mcp == nil {
		return "error: MCP tool dispatcher unavailable"
	}
	out, err := mcp(ctx, name, rawArgs)
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}
```

In `internal/chat/turn.go`, in the dispatch chain (~line 277), add a branch **before** the `CustomToolNames` branch:

```go
			} else if cfg.MCPToolNames[tc.Name] {
				out = dispatchMCPTool(ctx, cfg.MCPTool, tc.Name, tc.RawArgs)
			} else if cfg.CustomToolNames[tc.Name] {
```

(That turns the existing `} else if cfg.CustomToolNames[tc.Name] {` into the continuation shown above.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/chat/ -run TestDispatch -v`
Expected: PASS.

- [ ] **Step 6: Run the whole chat package**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/chat/
git commit -m "feat(chat): MCP tool dispatch seam routed before custom tools"
```

---

## Task 6: agentsetup wiring — build Manager, merge defs, set seam, shutdown

**Files:**
- Modify: `internal/agentsetup/agentsetup.go`

The builder constructs one `*mcp.Manager` from all declared servers, stores it for shutdown, merges per-agent prefixed tool defs into the active runtime, and sets the agent-independent `MCPTool` seam.

- [ ] **Step 1: Add a Manager field and builder step**

In `internal/agentsetup/agentsetup.go`, import the package:

```go
	"github.com/weatherjean/shell3/internal/mcp"
```

Add a field to the `builder` struct:

```go
	mcpMgr *mcp.Manager
```

Add a method that builds the manager from all declared servers (call it from wherever `loadConfig`/`openStore` are invoked in the build sequence — place the call right after `loadConfig` succeeds):

```go
// buildMCP constructs the MCP manager from all declared servers. Cache lives
// under the project dir so discovered schemas persist across runs.
func (b *builder) buildMCP() {
	servers := b.lc.MCPServers
	if len(servers) == 0 {
		return
	}
	specs := make([]mcp.Spec, 0, len(servers))
	for _, s := range servers {
		specs = append(specs, mcp.Spec{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			Tools:   s.Tools,
		})
	}
	cacheDir := filepath.Join(b.proj.Dir, "mcp")
	b.mcpMgr = mcp.NewManager(specs, cacheDir)
	b.closers = append(b.closers, func() { b.mcpMgr.Shutdown() })
}
```

> Note: confirm the project-dir field name with `grep -n "proj\b\|\.Dir\b\|Project" internal/agentsetup/agentsetup.go internal/paths/paths.go`. Use the field that yields the project directory (the one whose `DB` is `<dir>/shell3.db`). If it is `b.proj.Dir`, the code above is correct; otherwise substitute the right accessor.

Find the build sequence (where `loadConfig`, `openStore`, etc. are called in order) and add `b.buildMCP()` after `loadConfig` returns successfully and after `resolvePaths`/project UUID is known (so `b.proj` is populated).

- [ ] **Step 2: Merge per-agent MCP defs into the active runtime**

In `buildActiveRuntime` (after `toolDefs := luacfg.ToolDefs(...)` and the `toolNames` loop, ~line 178), add:

```go
	var mcpNames map[string]bool
	if b.mcpMgr != nil && len(a.MCPServerNames) > 0 {
		mcpDefs, err := b.mcpMgr.ToolDefinitionsFor(context.Background(), a.MCPServerNames)
		if err != nil {
			b.log.Warn("mcp: tool discovery failed; server tools unavailable", "error", err)
		} else {
			toolDefs = append(toolDefs, mcpDefs...)
			for _, d := range mcpDefs {
				toolNames = append(toolNames, d.Name)
			}
			mcpNames = b.mcpMgr.ToolNamesFor(a.MCPServerNames)
		}
	}
```

Add `MCPToolNames: mcpNames,` to the returned `chat.ActiveAgent{...}` literal (alongside `CustomToolNames`).

Ensure `context` is imported in this file (it likely is; if not, add `"context"`).

- [ ] **Step 3: Set the agent-independent seam in `assemble`**

In `assemble()`, where the `chat.Config{...}` literal is built (the block with `CustomTool: b.lc.CallTool`), add:

```go
		MCPTool: func(ctx context.Context, name, args string) (string, error) {
			if b.mcpMgr == nil {
				return "", fmt.Errorf("no MCP servers configured")
			}
			return b.mcpMgr.Dispatch(ctx, name, args)
		},
```

Ensure `fmt` and `context` are imported (likely already).

- [ ] **Step 4: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup/
git commit -m "feat(agentsetup): build MCP manager, merge prefixed defs, wire dispatch + shutdown"
```

---

## Task 7: Scaffold example + manual verification

**Files:**
- Modify: `internal/scaffold/defaults/shell3.lua`

- [ ] **Step 1: Add a commented MCP example to the default config**

In `internal/scaffold/defaults/shell3.lua`, after the custom-tool definitions and before the Skills section, add:

```lua
-- ---------------------------------------------------------------------------
-- MCP servers (optional)
-- ---------------------------------------------------------------------------
-- Connect to an external MCP server over stdio. Its tools are advertised to the
-- agent as `<name>__<tool>` (e.g. chrome__navigate_page). The server starts
-- lazily on first tool use; tool schemas are cached under .shell3/.../mcp/.
--
-- local chrome = shell3.mcp({
--   name    = "chrome",
--   command = "npx",
--   args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
--   -- tools = { "navigate_page", "click", "take_snapshot" }, -- optional allowlist
-- })
--
-- Then add `mcp = { chrome }` to an agent's `tools = { ... }` block.
```

- [ ] **Step 2: Verify the scaffold still loads**

Run: `go test ./internal/scaffold/`
Expected: PASS (the commented block must not break parsing; if scaffold tests load the config, uncommenting is not required).

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/defaults/shell3.lua
git commit -m "docs(scaffold): document optional shell3.mcp{} server block"
```

- [ ] **Step 4: Manual end-to-end check (requires Node + Chrome; optional)**

Create a scratch config that declares the chrome server (uncomment the block above into a test `shell3.lua`), add `mcp = { chrome }` to the agent, run `make build`, launch shell3, and ask the agent to call `chrome__navigate_page` then `chrome__take_snapshot`. Confirm a real browser session is driven and the snapshot returns. Record the result; this validates the full path against the real `chrome-devtools-mcp`.

---

## Task 8: Final validation and self-review

- [ ] **Step 1: Full build + test + vet**

Run:
```bash
make build && go test ./... && go vet ./...
```
Expected: build succeeds, all tests pass, vet clean.

- [ ] **Step 2: Confirm no MCP server is spawned unless a tool is called**

The unit tests already assert this (`TestManagerDispatchLazySessionReused`, `TestManagerDiscoveryUsesCacheSecondTime`). Confirm by reviewing: discovery probes once when cache is cold; sessions open only in `Dispatch`.

- [ ] **Step 3: Commit any fixes and report**

```bash
git add -A && git commit -m "test(mcp): final validation pass" --allow-empty
```

Summarize: files changed, tests added, commands run, and the manual-check result (or that it needs Node/Chrome to run).

---

## Self-Review (completed during planning)

- **Spec coverage:** stdio client (Task 2) ✓; discover-and-cache + lazy session + prefix (Task 3) ✓; `shell3.mcp{}` + `tools.mcp` (Task 4) ✓; dedicated `MCPTool`/`MCPToolNames` seam + guard-chain routing — routing added in Task 5, guard chain already wraps all dispatch in `turn.go` before this branch ✓; agentsetup wiring + shutdown (Task 6) ✓; tools-only, stdio-only, no resources/prompts/HTTP (scope honored) ✓; testing via fake server (Tasks 2–3) ✓.
- **Guard-chain note:** the existing `turn.go` guard/hook check runs ahead of the whole dispatch `if`-chain, so the new MCP branch is automatically guarded — no extra wiring needed. Verify during Task 5 that the MCP branch sits inside the same `if out == "" {` block as the other tools.
- **Type consistency:** `clientAPI` (Start/ListTools/CallTool/Close) matches `*Client`'s methods; `Manager` methods `ToolDefinitionsFor`/`ToolNamesFor`/`Dispatch`/`Shutdown` used consistently in Task 6; `mcp.Spec` fields match `luacfg.MCPServer` fields in the Task 6 bridge.
- **Placeholders:** none — every code step is complete. Two `grep`-to-confirm notes (luacfg test helper reuse; project-dir field name) are verification instructions, not placeholders.
