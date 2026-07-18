package luacfg

import (
	"path/filepath"
	"testing"
)

// loadMCPErr writes script as shell3.lua and expects Load to fail; returns the
// error message for content assertions.
func loadMCPErr(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", script)
	cfg, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		cfg.Close()
		t.Fatal("expected load error, got success")
	}
	return err.Error()
}

const mcpBase = `shell3.model("m", { base_url = "http://x/v1", api_key = "k", model = "z" })
shell3.agent({ name = "a", model = "m", prompt = "p" })
`

func TestMCPDeclStdio(t *testing.T) {
	cfg := mustLoad(t, mcpBase+`shell3.mcp({
  github = {
    command = { "gh-mcp", "--flag" },
    env = { TOKEN = "tok" },
    timeout = 30,
    allow = { "search", "get" },
  },
})`)
	servers := cfg.MCPServers()
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	s := servers[0]
	if s.Name != "github" || len(s.Command) != 2 || s.Command[0] != "gh-mcp" || s.Command[1] != "--flag" {
		t.Errorf("bad command parse: %+v", s)
	}
	if s.Env["TOKEN"] != "tok" || s.TimeoutSecs != 30 {
		t.Errorf("bad env/timeout: %+v", s)
	}
	if len(s.Allow) != 2 || s.Allow[0] != "search" || len(s.Deny) != 0 {
		t.Errorf("bad allow/deny: %+v", s)
	}
}

func TestMCPDeclHTTP(t *testing.T) {
	cfg := mustLoad(t, mcpBase+`shell3.mcp({
  linear = {
    url = "https://mcp.example.com/mcp",
    headers = { Authorization = "Bearer x" },
    deny = { "delete_issue" },
  },
})`)
	s := cfg.MCPServers()[0]
	if s.URL != "https://mcp.example.com/mcp" || s.Headers["Authorization"] != "Bearer x" {
		t.Errorf("bad http parse: %+v", s)
	}
	if len(s.Deny) != 1 || s.Deny[0] != "delete_issue" {
		t.Errorf("bad deny: %+v", s)
	}
}

func TestMCPDeclOrderIsDeterministic(t *testing.T) {
	cfg := mustLoad(t, mcpBase+`shell3.mcp({
  bbb = { url = "https://b/mcp" },
  aaa = { url = "https://a/mcp" },
  ccc = { url = "https://c/mcp" },
})`)
	servers := cfg.MCPServers()
	if len(servers) != 3 {
		t.Fatalf("want 3 servers, got %d", len(servers))
	}
	// Map iteration order in Lua tables is unspecified; we sort by name for
	// deterministic downstream behavior (connect order, status listings).
	if servers[0].Name != "aaa" || servers[1].Name != "bbb" || servers[2].Name != "ccc" {
		t.Errorf("want sorted by name, got %s,%s,%s", servers[0].Name, servers[1].Name, servers[2].Name)
	}
}

func TestMCPDeclErrors(t *testing.T) {
	cases := []struct{ name, body, wantErr string }{
		{"neither command nor url", `shell3.mcp({ x = { timeout = 5 } })`, "exactly one of command or url"},
		{"both command and url", `shell3.mcp({ x = { command = {"c"}, url = "https://u" } })`, "exactly one of command or url"},
		{"bad server name", `shell3.mcp({ ["Bad Name"] = { url = "https://u" } })`, "server name"},
		{"allow and deny", `shell3.mcp({ x = { url = "https://u", allow = {"a"}, deny = {"b"} } })`, "allow and deny"},
		{"second call", `shell3.mcp({ x = { url = "https://u" } })` + "\n" + `shell3.mcp({ y = { url = "https://u" } })`, "already declared"},
		{"unknown key", `shell3.mcp({ x = { url = "https://u", oauth = true } })`, "unknown key"},
		{"empty command", `shell3.mcp({ x = { command = {} } })`, "command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := loadMCPErr(t, mcpBase+tc.body)
			if !contains(msg, tc.wantErr) {
				t.Errorf("error %q does not mention %q", msg, tc.wantErr)
			}
		})
	}
}

func TestToolsMCPOptIn(t *testing.T) {
	cfg := mustLoad(t, `shell3.model("m", { base_url = "http://x/v1", api_key = "k", model = "z" })
shell3.mcp({
  github = { url = "https://g/mcp" },
  linear = { url = "https://l/mcp" },
})
shell3.agent({ name = "a", model = "m", prompt = "p", tools = { bash = true, mcp = { "github" } } })
shell3.subagent({ name = "s", description = "d", model = "m", prompt = "p", tools = { mcp = "all" } })
`)
	a := cfg.FirstAgent()
	if len(a.MCP) != 1 || a.MCP[0] != "github" || a.MCPAll {
		t.Errorf("agent opt-in wrong: MCP=%v MCPAll=%v", a.MCP, a.MCPAll)
	}
	if !a.Gates.Bash {
		t.Errorf("bash gate lost alongside mcp key")
	}
	sa, ok := cfg.SubagentByName("s")
	if !ok {
		t.Fatal("subagent missing")
	}
	if !sa.MCPAll || len(sa.MCP) != 0 {
		t.Errorf("subagent all opt-in wrong: MCP=%v MCPAll=%v", sa.MCP, sa.MCPAll)
	}
}

func TestToolsMCPOmittedMeansNone(t *testing.T) {
	cfg := mustLoad(t, mcpBase+`shell3.mcp({ github = { url = "https://g/mcp" } })`)
	a := cfg.FirstAgent()
	if len(a.MCP) != 0 || a.MCPAll {
		t.Errorf("omitted tools.mcp must mean none, got MCP=%v MCPAll=%v", a.MCP, a.MCPAll)
	}
}

func TestToolsMCPValidation(t *testing.T) {
	cases := []struct{ name, body, wantErr string }{
		{"unknown server", `shell3.mcp({ github = { url = "https://g/mcp" } })
shell3.agent({ name = "b", model = "m", prompt = "p", tools = { mcp = { "gitlab" } } })`, "unknown MCP server"},
		{"undeclared block", `shell3.agent({ name = "b", model = "m", prompt = "p", tools = { mcp = "all" } })`, "no shell3.mcp"},
		{"bad value type", `shell3.mcp({ github = { url = "https://g/mcp" } })
shell3.agent({ name = "b", model = "m", prompt = "p", tools = { mcp = 42 } })`, "tools.mcp"},
		{"bad string value", `shell3.mcp({ github = { url = "https://g/mcp" } })
shell3.agent({ name = "b", model = "m", prompt = "p", tools = { mcp = "some" } })`, "tools.mcp"},
	}
	pre := `shell3.model("m", { base_url = "http://x/v1", api_key = "k", model = "z" })` + "\n"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := loadMCPErr(t, pre+tc.body)
			if !contains(msg, tc.wantErr) {
				t.Errorf("error %q does not mention %q", msg, tc.wantErr)
			}
		})
	}
}
