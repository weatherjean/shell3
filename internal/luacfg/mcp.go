package luacfg

import (
	"fmt"
	"regexp"
	"sort"

	lua "github.com/yuin/gopher-lua"
)

// MCPServer is one declared MCP server from the top-level shell3.mcp{} block.
// Exactly one of Command (stdio) or URL (streamable HTTP) is set — enforced at
// load. Connection and tool listing are internal/mcp's job; luacfg only
// carries the declaration.
type MCPServer struct {
	Name        string
	Command     []string          // stdio child argv; empty when URL is set
	Env         map[string]string // extra environment for the stdio child
	URL         string            // streamable HTTP endpoint
	Headers     map[string]string // extra HTTP headers (e.g. Authorization)
	TimeoutSecs int               // connect+list and per-call timeout; 0 = default
	Allow, Deny []string          // tool-name filters; at most one list may be set
}

// MCPServers returns the declared MCP servers sorted by name. Lua table
// iteration order is unspecified, so sorting keeps connect order, status
// listings, and error messages deterministic across loads.
func (c *LoadedConfig) MCPServers() []MCPServer {
	out := make([]MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}

var mcpServerKeys = map[string]bool{
	"command": true, "env": true, "url": true, "headers": true,
	"timeout": true, "allow": true, "deny": true,
}

var mcpNameRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// luaMCP parses the top-level shell3.mcp{ name = {...}, ... } block. One block
// per config: a second call fails the load, like a second shell3.agent.
func (c *LoadedConfig) luaMCP(L *lua.LState) int {
	if c.mcpDeclared {
		L.RaiseError("shell3.mcp already declared (declare all servers in one block)")
	}
	c.mcpDeclared = true
	opts := L.CheckTable(1)
	opts.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok || !mcpNameRE.MatchString(string(name)) {
			L.RaiseError("shell3.mcp: server name %q invalid (must match [a-z0-9_-]+)", k.String())
		}
		st, ok := v.(*lua.LTable)
		if !ok {
			L.RaiseError("shell3.mcp: server %q must be a table", string(name))
		}
		c.mcpServers = append(c.mcpServers, parseMCPServer(L, string(name), st))
	})
	sort.Slice(c.mcpServers, func(i, j int) bool { return c.mcpServers[i].Name < c.mcpServers[j].Name })
	return 0
}

// parseMCPServer validates and extracts one server table.
func parseMCPServer(L *lua.LState, name string, st *lua.LTable) MCPServer {
	ctx := fmt.Sprintf("shell3.mcp server %q", name)
	mustKeys(L, st, ctx, mcpServerKeys)
	s := MCPServer{
		Name:        name,
		URL:         optStr(st, "url"),
		TimeoutSecs: optInt(st, "timeout"),
	}
	if ct, ok := st.RawGetString("command").(*lua.LTable); ok {
		s.Command = stringList(ct)
		if len(s.Command) == 0 {
			L.RaiseError("%s: command must be a non-empty list of strings", ctx)
		}
	}
	if (len(s.Command) == 0) == (s.URL == "") {
		L.RaiseError("%s: set exactly one of command or url", ctx)
	}
	if et, ok := st.RawGetString("env").(*lua.LTable); ok {
		s.Env = stringMapStrict(L, et, ctx+".env")
	}
	if ht, ok := st.RawGetString("headers").(*lua.LTable); ok {
		s.Headers = stringMapStrict(L, ht, ctx+".headers")
	}
	if at, ok := st.RawGetString("allow").(*lua.LTable); ok {
		s.Allow = stringList(at)
	}
	if dt, ok := st.RawGetString("deny").(*lua.LTable); ok {
		s.Deny = stringList(dt)
	}
	if len(s.Allow) > 0 && len(s.Deny) > 0 {
		L.RaiseError("%s: set at most one of allow and deny", ctx)
	}
	return s
}

// stringMapStrict reads a Lua table as a string→string map, raising on any
// non-string key or value: env vars and HTTP headers silently dropped or
// coerced would be a debugging trap (a missing Authorization header just 401s).
func stringMapStrict(L *lua.LState, t *lua.LTable, ctx string) map[string]string {
	out := map[string]string{}
	t.ForEach(func(k, v lua.LValue) {
		ks, kok := k.(lua.LString)
		vs, vok := v.(lua.LString)
		if !kok || !vok {
			L.RaiseError("%s: keys and values must be strings", ctx)
		}
		out[string(ks)] = string(vs)
	})
	return out
}

// parseToolsMCP interprets the raw tools.mcp value for an agent or subagent:
// a list of declared server names, or the string "all". ctx labels errors.
func parseToolsMCP(L *lua.LState, raw lua.LValue, ctx string) (names []string, all bool) {
	switch v := raw.(type) {
	case *lua.LNilType:
		return nil, false
	case lua.LString:
		if string(v) != "all" {
			L.RaiseError(`%s: tools.mcp must be a list of server names or the string "all"`, ctx)
		}
		return nil, true
	case *lua.LTable:
		for i := 1; i <= v.Len(); i++ {
			s, ok := v.RawGetInt(i).(lua.LString)
			if !ok {
				L.RaiseError("%s: tools.mcp[%d] must be a server name string", ctx, i)
			}
			names = append(names, string(s))
		}
		return names, false
	default:
		L.RaiseError(`%s: tools.mcp must be a list of server names or the string "all"`, ctx)
		return nil, false
	}
}

// validateMCPRefs checks every agent's/subagent's tools.mcp against the
// declared servers. Runs post-parse (from load) so declaration order between
// shell3.mcp{} and the agents never matters.
func (c *LoadedConfig) validateMCPRefs() error {
	byName := map[string]bool{}
	for _, s := range c.mcpServers {
		byName[s.Name] = true
	}
	check := func(kind, name string, mcp []string, all bool) error {
		if (all || len(mcp) > 0) && len(c.mcpServers) == 0 {
			return fmt.Errorf("config: %s %q sets tools.mcp but no shell3.mcp{} block is declared", kind, name)
		}
		for _, n := range mcp {
			if !byName[n] {
				return fmt.Errorf("config: %s %q references unknown MCP server %q", kind, name, n)
			}
		}
		return nil
	}
	for _, a := range c.agents {
		if err := check("agent", a.Name, a.MCP, a.MCPAll); err != nil {
			return err
		}
	}
	for _, s := range c.subagents {
		if err := check("subagent", s.Name, s.MCP, s.MCPAll); err != nil {
			return err
		}
	}
	return nil
}
