package luacfg

import "testing"

func TestMCPServerParsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local chrome = shell3.mcp({
  name = "chrome",
  command = "npx",
  args = { "-y", "chrome-devtools-mcp@latest" },
  tools = { "navigate_page" },
})
shell3.agent({ name="base", model="m", prompt="p",
  tools = { bash = true, mcp = { chrome } } })
`)
	lc, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

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
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.mcp({ name = "bad" })
shell3.agent({ name="base", model="m", prompt="p", tools = { bash = true } })
`)
	if _, err := Load(dir+"/shell3.lua", dir); err == nil {
		t.Fatalf("expected error for missing command")
	}
}
