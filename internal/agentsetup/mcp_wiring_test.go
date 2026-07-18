package agentsetup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeMCPTree writes a config tree into ~/.shell3 under a temp home:
// wiringYAML (the model + the given mcp/agent frontmatter pieces) as
// shell3.yaml, agent.md with the given frontmatter mcp opt-in, and optionally
// a subagent.
func writeMCPTree(t *testing.T, yamlText, agentMD string, extra map[string]string) (configDir, cwd, home string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	configDir = filepath.Join(home, ".shell3")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"shell3.yaml": yamlText, "agent.md": agentMD}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		fp := filepath.Join(configDir, name)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return configDir, cwd, home
}

const wiringBase = "models:\n  m: { base_url: \"http://x/v1\", api_key: k, model: z }\n"

func TestMCPWiringLiveServer(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "echo back",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil
	})
	hs := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server { return srv }, nil))
	t.Cleanup(hs.Close)

	yamlText := wiringBase + fmt.Sprintf("mcp:\n  fake: { url: %q }\n", hs.URL)
	configDir, cwd, home := writeMCPTree(t, yamlText,
		"---\nmodel: m\ntools: [bash]\nmcp: all\n---\np\n",
		map[string]string{"agents/s.md": "---\ndescription: d\n---\np\n"})
	p, cleanup, err := BuildParts(Options{ConfigDir: configDir, CWD: cwd, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	// Agent opted in via "all": mcp_fake_echo is advertised + host-routed.
	rt, err := p.AgentRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range rt.Personality.Tools {
		if d.Name == "mcp_fake_echo" {
			found = true
			if d.Description != "echo back" {
				t.Errorf("description not passed through: %q", d.Description)
			}
		}
	}
	if !found {
		t.Fatalf("mcp_fake_echo missing from agent tools: %+v", rt.ActiveTools)
	}
	if !rt.HostToolNames["mcp_fake_echo"] {
		t.Error("mcp_fake_echo not routed to host-tool dispatch")
	}

	// Subagent did NOT opt in: no MCP tools, no host routing.
	srt, err := p.AgentRuntime("s")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range srt.Personality.Tools {
		if strings.HasPrefix(d.Name, "mcp_") {
			t.Errorf("subagent must not get MCP tools, has %q", d.Name)
		}
	}
	if srt.HostToolNames["mcp_fake_echo"] {
		t.Error("subagent must not host-route MCP tools")
	}

	// Dispatch through the session config's HostTool round-trips the call.
	cfg, err := p.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostTool == nil {
		t.Fatal("cfg.HostTool not wired")
	}
	out, err := cfg.HostTool(context.Background(), "mcp_fake_echo", `{"msg":"hi"}`)
	if err != nil || out != "pong" {
		t.Fatalf("HostTool call: %q %v", out, err)
	}
	if _, err := cfg.HostTool(context.Background(), "not_a_tool", `{}`); err == nil {
		t.Error("unowned name must error (ErrHostToolNotFound path)")
	}

	// Status reports the server up with one tool — via Parts and via the
	// session config's dashboard closure.
	st := p.MCPStatus()
	if len(st) != 1 || !st[0].Up || st[0].ToolCount != 1 {
		t.Errorf("bad MCPStatus: %+v", st)
	}
	if cfg.MCPStatus == nil {
		t.Fatal("cfg.MCPStatus not wired")
	}
	if cst := cfg.MCPStatus(); len(cst) != 1 || !cst[0].Up || cst[0].ToolCount != 1 || cst[0].Name != "fake" {
		t.Errorf("bad cfg.MCPStatus: %+v", cst)
	}
}

func TestMCPWiringDownServer(t *testing.T) {
	yamlText := wiringBase + "mcp:\n  dead: { command: [\"/nonexistent-mcp-server-xyz\"], timeout: 2 }\n"
	configDir, cwd, home := writeMCPTree(t, yamlText, "---\nmodel: m\nmcp: all\n---\np\n", nil)
	p, cleanup, err := BuildParts(Options{ConfigDir: configDir, CWD: cwd, HomeDir: home})
	if err != nil {
		t.Fatalf("down server must not fail the build: %v", err)
	}
	t.Cleanup(cleanup)

	cfg, err := p.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var warned bool
	for _, w := range cfg.ConfigWarnings {
		if strings.Contains(w, "dead") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("down server missing from ConfigWarnings: %v", cfg.ConfigWarnings)
	}
	rt, _ := p.AgentRuntime("")
	for _, d := range rt.Personality.Tools {
		if strings.HasPrefix(d.Name, "mcp_") {
			t.Errorf("down server must contribute no tools, got %q", d.Name)
		}
	}
	st := p.MCPStatus()
	if len(st) != 1 || st[0].Up || st[0].Err == "" {
		t.Errorf("bad MCPStatus for down server: %+v", st)
	}
}

func TestMCPWiringAbsent(t *testing.T) {
	configDir, cwd, home := writeMCPTree(t, wiringBase, "---\nmodel: m\n---\np\n", nil)
	p, cleanup, err := BuildParts(Options{ConfigDir: configDir, CWD: cwd, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if p.MCPStatus() != nil {
		t.Error("no shell3.mcp{} must mean nil status")
	}
	cfg, err := p.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostTool != nil {
		t.Error("no MCP block must leave cfg.HostTool nil (RegisterHostTool owns it)")
	}
}
