package agentsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weatherjean/shell3/internal/chat"
)

// writeMCPTree writes a kit into ~/.shell3 under a temp home: wiringYAML
// becomes the kit's `shell3:` block, mainOpts are extra frontmatter lines on
// the main agent (the mcp: opt-in), and employee, when non-empty, adds a
// second agent by that name with no opt-in.
func writeMCPTree(t *testing.T, wiring string, mainOpts []string, employee string) (configDir, cwd, home string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	configDir = filepath.Join(home, ".shell3")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("#---\n# shell3:\n")
	for _, line := range strings.Split(strings.TrimRight(wiring, "\n"), "\n") {
		b.WriteString("#   " + line + "\n")
	}
	b.WriteString("#---\n\n#---\n# agent: main\n# model: m\n")
	for _, o := range mainOpts {
		b.WriteString("# " + o + "\n")
	}
	b.WriteString("#---\nmain_prompt() { cat <<'SHELL3_EOF'\np\nSHELL3_EOF\n}\n")
	if employee != "" {
		b.WriteString("\n#---\n# agent: " + employee + "\n# description: d\n# model: m\n#---\n" +
			employee + "_prompt() { cat <<'SHELL3_EOF'\np\nSHELL3_EOF\n}\n")
	}
	if err := os.WriteFile(filepath.Join(configDir, "shell3.sh"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
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
	configDir, cwd, home := writeMCPTree(t, yamlText, []string{"use: [bash]", "mcp: all"}, "s")
	p, cleanup, err := BuildParts(Options{ConfigDir: configDir, CWD: cwd, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

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

	srt, err := p.AgentRuntime("s")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range srt.Personality.Tools {
		if strings.HasPrefix(d.Name, "mcp_") {
			t.Errorf("employee must not get MCP tools, has %q", d.Name)
		}
	}
	if srt.HostToolNames["mcp_fake_echo"] {
		t.Error("employee must not host-route MCP tools")
	}

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
	configDir, cwd, home := writeMCPTree(t, yamlText, []string{"mcp: all"}, "")
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
	configDir, cwd, home := writeMCPTree(t, wiringBase, nil, "")
	p, cleanup, err := BuildParts(Options{ConfigDir: configDir, CWD: cwd, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if p.MCPStatus() != nil {
		t.Error("no mcp: block must mean nil status")
	}
	cfg, err := p.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The kit's own tool dispatcher always owns HostTool; with no MCP block
	// and no declared tools, every name falls through to ErrHostToolNotFound.
	if cfg.HostTool == nil {
		t.Fatal("cfg.HostTool should be the kit dispatcher")
	}
	if _, err := cfg.HostTool(context.Background(), "mcp_fake_echo", `{}`); !errors.Is(err, chat.ErrHostToolNotFound) {
		t.Errorf("unowned name err = %v, want ErrHostToolNotFound", err)
	}
}
