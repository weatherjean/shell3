package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/config"
)

// fakeServer builds an in-process SDK server exposing the given tools, each
// returning fixed content. Returned dial connects a fresh in-memory session
// per call (so renewal tests get a live server again) and records the server
// sessions it opened.
type fakeServer struct {
	srv      *sdk.Server
	mu       sync.Mutex
	sessions []*sdk.ServerSession
	dials    int
}

func newFakeServer(name string, tools map[string]sdk.ToolHandler) *fakeServer {
	return newFakeServerOpts(name, nil, tools)
}

func newFakeServerOpts(name string, opts *sdk.ServerOptions, tools map[string]sdk.ToolHandler) *fakeServer {
	srv := sdk.NewServer(&sdk.Implementation{Name: name}, opts)
	for tn, h := range tools {
		srv.AddTool(&sdk.Tool{
			Name:        tn,
			Description: "fake " + tn,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"msg": map[string]any{"type": "string"}},
			},
		}, h)
	}
	return &fakeServer{srv: srv}
}

func textHandler(out string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}, nil
	}
}

func (f *fakeServer) dial(ctx context.Context, _ config.MCPServer) (sdk.Transport, error) {
	ct, st := sdk.NewInMemoryTransports()
	ss, err := f.srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.sessions = append(f.sessions, ss)
	f.dials++
	f.mu.Unlock()
	return ct, nil
}

// killSessions closes every server-side session, simulating a died server.
func (f *fakeServer) killSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		_ = s.Close()
	}
	f.sessions = nil
}

func newTestManager(t *testing.T, servers []config.MCPServer, dial dialFunc) *Manager {
	t.Helper()
	m := New(servers, applog.Noop{})
	m.dial = dial
	t.Cleanup(m.Close)
	return m
}

func TestConnectListAndToolDefs(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{
		"search": textHandler("found"),
		"get":    textHandler("got"),
	})
	m := newTestManager(t, []config.MCPServer{{Name: "github", URL: "x"}}, fs.dial)
	warns := m.Connect(context.Background())
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	defs := m.Tools([]string{"github"}, false)
	if len(defs) != 2 {
		t.Fatalf("want 2 defs, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.Parameters["type"] != "object" {
			t.Errorf("inputSchema not passed through: %v", d.Parameters)
		}
	}
	if !names["mcp_github_search"] || !names["mcp_github_get"] {
		t.Errorf("bad prefixed names: %v", names)
	}
	// Opt-in filtering: unknown server or none selected → no defs.
	if got := m.Tools(nil, false); len(got) != 0 {
		t.Errorf("no opt-in must yield no defs, got %v", got)
	}
	if got := m.Tools(nil, true); len(got) != 2 {
		t.Errorf("all=true must yield every def, got %d", len(got))
	}
}

func TestAllowDenyFilter(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{
		"search": textHandler("found"),
		"del":    textHandler("deleted"),
	})
	allow := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x", Allow: []string{"search"}}}, fs.dial)
	allow.Connect(context.Background())
	if defs := allow.Tools(nil, true); len(defs) != 1 || defs[0].Name != "mcp_g_search" {
		t.Errorf("allow filter wrong: %v", defs)
	}
	if !allow.Owns("mcp_g_search") || allow.Owns("mcp_g_del") {
		t.Error("Owns must reflect the allow filter")
	}
	deny := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x", Deny: []string{"del"}}}, fs.dial)
	deny.Connect(context.Background())
	if defs := deny.Tools(nil, true); len(defs) != 1 || defs[0].Name != "mcp_g_search" {
		t.Errorf("deny filter wrong: %v", defs)
	}
}

func TestCallHappyPath(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"echo": func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var args map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			t.Errorf("args: %v", err)
		}
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "echo: " + args["msg"].(string)},
			&sdk.TextContent{Text: "second"},
		}}, nil
	}})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	out, err := m.Call(context.Background(), "mcp_g_echo", `{"msg":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo: hi\nsecond" {
		t.Errorf("bad flatten: %q", out)
	}
}

// TestListToolsPagination proves connect follows nextCursor: with a server
// page size of 1, all three tools must still be indexed.
func TestListToolsPagination(t *testing.T) {
	fs := newFakeServerOpts("gh", &sdk.ServerOptions{PageSize: 1}, map[string]sdk.ToolHandler{
		"a": textHandler("a"), "b": textHandler("b"), "c": textHandler("c"),
	})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	if warns := m.Connect(context.Background()); len(warns) > 0 {
		t.Fatalf("warns: %v", warns)
	}
	defs := m.Tools(nil, true)
	if len(defs) != 3 {
		t.Fatalf("got %d tools, want 3 (pagination not followed): %+v", len(defs), defs)
	}
}

// TestCallStructuredContentOnly covers a spec-legal result that carries only
// structuredContent (mirroring into a text block is a SHOULD, not a MUST).
func TestCallStructuredContentOnly(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"stats": func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{StructuredContent: map[string]any{"count": 42}}, nil
	}})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	out, err := m.Call(context.Background(), "mcp_g_stats", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"count":42`) {
		t.Errorf("structuredContent lost: %q", out)
	}
}

func TestCallToolError(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"bad": func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "boom"}}}, nil
	}})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	out, err := m.Call(context.Background(), "mcp_g_bad", `{}`)
	if err != nil {
		t.Fatalf("isError must not be a Go error: %v", err)
	}
	if !strings.Contains(out, "boom") || !strings.Contains(out, "error") {
		t.Errorf("tool error not surfaced as text: %q", out)
	}
}

func TestCallUnknownTool(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"echo": textHandler("e")})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	if _, err := m.Call(context.Background(), "mcp_g_nope", `{}`); err == nil {
		t.Error("unknown tool must return a Go error")
	}
	if _, err := m.Call(context.Background(), "read_file", `{}`); err == nil {
		t.Error("non-mcp name must return a Go error")
	}
}

func TestReconnectOnce(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"echo": textHandler("ok")})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	if out, err := m.Call(context.Background(), "mcp_g_echo", `{}`); err != nil || out != "ok" {
		t.Fatalf("first call: %q %v", out, err)
	}
	fs.killSessions() // server dies
	out, err := m.Call(context.Background(), "mcp_g_echo", `{}`)
	if err != nil {
		t.Fatalf("call after kill should renew: %v", err)
	}
	if out != "ok" {
		t.Errorf("renewed call result: %q", out)
	}
	if fs.dials < 2 {
		t.Errorf("expected a re-dial, got %d dials", fs.dials)
	}
}

func TestDownServerIsWarningNotError(t *testing.T) {
	badDial := func(ctx context.Context, s config.MCPServer) (sdk.Transport, error) {
		return nil, context.DeadlineExceeded
	}
	m := newTestManager(t, []config.MCPServer{{Name: "dead", URL: "x", TimeoutSecs: 1}}, badDial)
	warns := m.Connect(context.Background())
	if len(warns) != 1 || !strings.Contains(warns[0], "dead") {
		t.Fatalf("want one warning naming the server, got %v", warns)
	}
	if defs := m.Tools(nil, true); len(defs) != 0 {
		t.Errorf("down server must contribute no tools: %v", defs)
	}
	st := m.Status()
	if len(st) != 1 || st[0].Up || st[0].Err == "" {
		t.Errorf("bad status for down server: %+v", st)
	}
}

func TestStatus(t *testing.T) {
	fs := newFakeServer("gh", map[string]sdk.ToolHandler{"a": textHandler("x"), "b": textHandler("y")})
	m := newTestManager(t, []config.MCPServer{{Name: "g", URL: "x"}}, fs.dial)
	m.Connect(context.Background())
	st := m.Status()
	if len(st) != 1 || !st[0].Up || st[0].ToolCount != 2 || st[0].Name != "g" {
		t.Errorf("bad status: %+v", st)
	}
}
