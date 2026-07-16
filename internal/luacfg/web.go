package luacfg

import lua "github.com/yuin/gopher-lua"

// WebConfig is the parsed top-level shell3.web{} block — the standalone web
// front-end (shell3 web): the dashboard plus chat, served over plain HTTP with
// token auth instead of Telegram initData. Secret gates every API call
// (X-Auth-Token header / ?key= param). Tunnel and URL have the same semantics
// as telegram.dashboard: URL is a fixed public address; Tunnel is a command
// spawned at start ({addr} replaced) whose output is scanned for an https URL.
type WebConfig struct {
	Addr   string
	Secret string
	URL    string
	Tunnel string
}

// Web returns the parsed shell3.web{} block (zero value if absent).
func (c *LoadedConfig) Web() WebConfig { return c.web }

var webKeys = map[string]bool{"addr": true, "secret": true, "url": true, "tunnel": true}

func (c *LoadedConfig) luaWeb(L *lua.LState) int {
	opts := L.CheckTable(1)
	mustKeys(L, opts, "web", webKeys)
	c.web = WebConfig{
		Addr:   optStr(opts, "addr"),
		Secret: optStr(opts, "secret"),
		URL:    optStr(opts, "url"),
		Tunnel: optStr(opts, "tunnel"),
	}
	return 0
}
