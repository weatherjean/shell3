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
