// Package mcp is shell3's MCP client: tools only, stdio and streamable HTTP,
// on the official go-sdk. A Manager connects every declared server
// synchronously at build time, lists tools once, and dispatches with one
// reconnect retry. No OAuth, resources, prompts, sampling or SSE, and
// ToolListChanged is ignored — the list is static until the next /reload.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

const defaultTimeout = 10 * time.Second

// ToolPrefix + "<server>_<tool>" is every MCP tool's model-facing name. The
// kit's gate matches on it, so the prefix is public config surface.
const ToolPrefix = "mcp_"

// toolName is the one place a server's tool gets its model-facing name.
func toolName(server, tool string) string { return ToolPrefix + server + "_" + tool }

// ServerStatus is one server's health for `shell3 health` and the Status view.
type ServerStatus struct {
	Name      string `json:"name"`
	Up        bool   `json:"up"`
	ToolCount int    `json:"tool_count"`
	Err       string `json:"err,omitempty"`
}

// dialFunc opens a server's transport, on connect and on each renewal;
// swapped for in-memory transports in tests.
type dialFunc func(ctx context.Context, s config.MCPServer) (sdk.Transport, error)

// serverConn is one server's live state; mu serializes its connect, renewal
// and calls. Stdio sessions are sequential anyway, and one agent rarely
// benefits from parallel HTTP calls.
type serverConn struct {
	cfg     config.MCPServer
	mu      sync.Mutex
	sess    *sdk.ClientSession
	tools   []*sdk.Tool // post allow/deny filter
	up      bool
	lastErr error
}

func (sc *serverConn) timeout() time.Duration {
	if sc.cfg.TimeoutSecs > 0 {
		return time.Duration(sc.cfg.TimeoutSecs) * time.Second
	}
	return defaultTimeout
}

// Manager owns the client sessions for every declared MCP server.
type Manager struct {
	servers []*serverConn // declaration (sorted) order
	byTool  map[string]*toolRoute
	mu      sync.RWMutex // guards byTool (rebuilt on renewal)
	log     applog.Logger
	dial    dialFunc
}

type toolRoute struct {
	sc   *serverConn
	tool string // unprefixed server-side name
}

// New builds a Manager for the declared servers. Call Connect before use.
func New(servers []config.MCPServer, log applog.Logger) *Manager {
	if log == nil {
		log = applog.Noop{}
	}
	m := &Manager{byTool: map[string]*toolRoute{}, log: log}
	for _, s := range servers {
		m.servers = append(m.servers, &serverConn{cfg: s})
	}
	m.dial = defaultDial
	return m
}

// defaultDial builds the real transport for a server declaration.
func defaultDial(_ context.Context, s config.MCPServer) (sdk.Transport, error) {
	if len(s.Command) > 0 {
		cmd := exec.Command(s.Command[0], s.Command[1:]...)
		cmd.Env = append(os.Environ(), envList(s.Env)...)
		// Its own process group, so a terminal SIGINT to shell3 misses the
		// server; shutdown stays the SDK's stdin-close then SIGTERM.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return &sdk.CommandTransport{Command: cmd}, nil
	}
	client := &http.Client{}
	if len(s.Headers) > 0 {
		client.Transport = &headerTransport{headers: s.Headers, base: http.DefaultTransport}
	}
	return &sdk.StreamableClientTransport{
		Endpoint:   s.URL,
		HTTPClient: client,
		// Tools-only: nothing consumes server-initiated notifications, so skip
		// the standalone SSE stream, which some servers mishandle anyway.
		DisableStandaloneSSE: true,
	}, nil
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// headerTransport injects the configured headers — how bearer-token auth is
// expressed: headers = { Authorization = "Bearer …" }.
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// Connect dials every server in parallel under its own timeout, lists tools,
// applies allow/deny, and indexes the prefixed names. A server that fails is
// left down with a warning: the config still loads and its tools are absent
// until the next reload. A name collision is also a warning, first
// registration winning deterministically — it is only detectable after a
// remote ListTools, and failing hard would let a remote rename brick a reload.
func (m *Manager) Connect(ctx context.Context) []string {
	if ctx == nil { // e.g. a cobra command without an executed root
		ctx = context.Background()
	}
	var wg sync.WaitGroup
	for _, sc := range m.servers {
		wg.Add(1)
		go func(sc *serverConn) {
			defer wg.Done()
			sc.mu.Lock()
			defer sc.mu.Unlock()
			m.connectLocked(ctx, sc)
		}(sc)
	}
	wg.Wait()

	var warns []string
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sc := range m.servers {
		if !sc.up {
			warns = append(warns, fmt.Sprintf("mcp server %q: %v", sc.cfg.Name, sc.lastErr))
			continue
		}
		for _, t := range sc.tools {
			full := toolName(sc.cfg.Name, t.Name)
			if prev, dup := m.byTool[full]; dup {
				// Ambiguous dispatch — server "a" tool "b_c" against server
				// "a_b" tool "c". Drop the later one with a warning; servers
				// are name-sorted, so the winner is deterministic.
				warns = append(warns, fmt.Sprintf(
					"mcp: tool name collision on %q (server %q tool %q vs server %q tool %q) — keeping the first",
					full, prev.sc.cfg.Name, prev.tool, sc.cfg.Name, t.Name))
				continue
			}
			m.byTool[full] = &toolRoute{sc: sc, tool: t.Name}
		}
	}
	return warns
}

// connectLocked dials + initializes + lists one server. Caller holds sc.mu.
func (m *Manager) connectLocked(ctx context.Context, sc *serverConn) {
	cctx, cancel := context.WithTimeout(ctx, sc.timeout())
	defer cancel()
	transport, err := m.dial(cctx, sc.cfg)
	if err != nil {
		sc.up, sc.lastErr = false, err
		return
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "shell3", Version: "1"}, nil)
	sess, err := client.Connect(cctx, transport, nil)
	if err != nil {
		sc.up, sc.lastErr = false, err
		return
	}
	// sess.Tools follows nextCursor; a plain ListTools stops at page one.
	var tools []*sdk.Tool
	for t, err := range sess.Tools(cctx, nil) {
		if err != nil {
			_ = sess.Close()
			sc.up, sc.lastErr = false, fmt.Errorf("list tools: %w", err)
			return
		}
		tools = append(tools, t)
	}
	sc.sess = sess
	sc.tools = filterTools(tools, sc.cfg.Allow, sc.cfg.Deny)
	sc.up, sc.lastErr = true, nil
	m.log.Debug("mcp server connected", "server", sc.cfg.Name, "tools", len(sc.tools))
}

// filterTools applies the allow/deny lists; at most one is set, per load.
func filterTools(tools []*sdk.Tool, allow, deny []string) []*sdk.Tool {
	var out []*sdk.Tool
	for _, t := range tools {
		if len(allow) > 0 && !slices.Contains(allow, t.Name) {
			continue
		}
		if slices.Contains(deny, t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Tools is the llm definitions for the opted-in servers, or every declared one
// when all is set. Down servers contribute nothing.
func (m *Manager) Tools(serverNames []string, all bool) []llm.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var defs []llm.ToolDefinition
	for _, sc := range m.servers {
		if !all && !slices.Contains(serverNames, sc.cfg.Name) {
			continue
		}
		if !sc.up {
			continue
		}
		for _, t := range sc.tools {
			full := toolName(sc.cfg.Name, t.Name)
			if r, ok := m.byTool[full]; !ok || r.sc != sc {
				continue // lost a collision — not dispatchable, so not advertised
			}
			defs = append(defs, llm.ToolDefinition{
				Name:        full,
				Description: t.Description,
				Parameters:  schemaMap(t.InputSchema),
			})
		}
	}
	return defs
}

// schemaMap normalizes the SDK's InputSchema to what the llm layer
// serializes. The client side already unmarshals to a map; anything else
// round-trips through JSON, and a nil or failed schema becomes an empty
// object schema, which is spec-valid.
func schemaMap(s any) map[string]any {
	if m, ok := s.(map[string]any); ok {
		return m
	}
	if s != nil {
		if b, err := json.Marshal(s); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m != nil {
				return m
			}
		}
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// Owns reports a dispatchable tool: connected, not collision-lost, not
// filtered.
func (m *Manager) Owns(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.byTool[name]
	return ok
}

// Call dispatches a prefixed tool call and returns the flattened text.
// Transport failures get ONE renewal and retry; a still-failing call, and a
// tool-level isError, come back as error TEXT with a nil Go error, so a broken
// server cannot kill a turn. The only Go error is an unknown tool name.
func (m *Manager) Call(ctx context.Context, name, argsJSON string) (string, error) {
	m.mu.RLock()
	r, ok := m.byTool[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown MCP tool %q", name)
	}
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("MCP tool error: invalid arguments JSON: %v", err), nil
		}
	}
	sc := r.sc
	sc.mu.Lock()
	defer sc.mu.Unlock()
	res, err := m.callLocked(ctx, sc, r.tool, args)
	if err != nil {
		// The stdio child may have died or the HTTP session expired: re-dial,
		// re-list, retry once.
		m.log.Warn("mcp call failed, renewing session", "server", sc.cfg.Name, "tool", r.tool, "error", err)
		if sc.sess != nil {
			_ = sc.sess.Close()
			sc.sess = nil
		}
		m.connectLocked(ctx, sc)
		if !sc.up {
			return fmt.Sprintf("MCP tool error: server %q unavailable: %v", sc.cfg.Name, sc.lastErr), nil
		}
		if res, err = m.callLocked(ctx, sc, r.tool, args); err != nil {
			return fmt.Sprintf("MCP tool error: %v", err), nil
		}
	}
	text := flatten(res)
	if res.IsError {
		return "MCP tool error: " + text, nil
	}
	return text, nil
}

// callLocked performs one CallTool under the server's timeout. Caller holds sc.mu.
func (m *Manager) callLocked(ctx context.Context, sc *serverConn, tool string, args map[string]any) (*sdk.CallToolResult, error) {
	if sc.sess == nil {
		return nil, fmt.Errorf("server %q not connected", sc.cfg.Name)
	}
	cctx, cancel := context.WithTimeout(ctx, sc.timeout())
	defer cancel()
	return sc.sess.CallTool(cctx, &sdk.CallToolParams{Name: tool, Arguments: args})
}

// flatten joins a result's text parts, marking non-text ones so the model
// knows something was there — this surface is text-only, and read_media covers
// local files rather than MCP blobs. A result whose only payload is
// structuredContent, which is spec-legal, is serialized as JSON so it is not
// lost.
func flatten(res *sdk.CallToolResult) string {
	var parts []string
	hasText := false
	for _, c := range res.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, v.Text)
			hasText = true
		case *sdk.ImageContent:
			parts = append(parts, "[image content omitted]")
		case *sdk.AudioContent:
			parts = append(parts, "[audio content omitted]")
		default:
			parts = append(parts, "[non-text content omitted]")
		}
	}
	if !hasText && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			parts = append(parts, string(b))
		}
	}
	if len(parts) == 0 {
		return "(empty result)"
	}
	return strings.Join(parts, "\n")
}

// Status reports every declared server's health, in name order.
func (m *Manager) Status() []ServerStatus {
	out := make([]ServerStatus, 0, len(m.servers))
	for _, sc := range m.servers {
		sc.mu.Lock()
		st := ServerStatus{Name: sc.cfg.Name, Up: sc.up, ToolCount: len(sc.tools)}
		if sc.lastErr != nil {
			st.Err = sc.lastErr.Error()
		}
		if !sc.up {
			st.ToolCount = 0
		}
		sc.mu.Unlock()
		out = append(out, st)
	}
	return out
}

// Close closes every session; CommandTransport terminates its stdio child.
func (m *Manager) Close() {
	for _, sc := range m.servers {
		sc.mu.Lock()
		if sc.sess != nil {
			_ = sc.sess.Close()
			sc.sess = nil
		}
		sc.up = false
		sc.mu.Unlock()
	}
}
