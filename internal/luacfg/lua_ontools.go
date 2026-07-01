package luacfg

import (
	"context"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
	ActionAsk
)

// ToolCallVerdict is the result of running the on_tool_call chain.
type ToolCallVerdict struct {
	Action     ToolCallAction
	Argv       []string      // ActionRun: exec exactly this
	Prompt     string        // ActionAsk: human prompt
	Reason     string        // ActionBlock reason, or ActionAsk deny-reason
	AskTimeout time.Duration // ActionAsk: 0 = caller default
	// Passthrough is true only on ActionRun when NO handler produced a
	// command/argv verdict — a pure fall-through where the chain expressed no
	// opinion. It lets the non-bash gate distinguish "no handler touched this"
	// (allow) from an actual {command=...}/{argv=...} verdict (which applies only
	// to bash tools and must fail closed), without inferring intent from the
	// argv's byte shape — a {command=""} rewrite yields the same argv as a pass.
	Passthrough bool
}

// RunToolCall runs the on_tool_call chain for one tool invocation and returns
// the verdict. It locks the VM (gopher-lua is single-threaded) and returns fast:
// an ask verdict defers the human prompt to the caller, so the lock is never
// held across a human wait. FAILS CLOSED — a Lua error or malformed return
// blocks rather than runs.
func (c *LoadedConfig) RunToolCall(ctx context.Context, name, command, argsJSON string) ToolCallVerdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	cmd := command
	rewritten := false
	for _, fn := range c.onToolCall {
		t := c.L.NewTable()
		c.L.SetField(t, "name", lua.LString(name))
		if cmd != "" {
			c.L.SetField(t, "command", lua.LString(cmd))
		}
		c.L.SetField(t, "args", lua.LString(argsJSON))
		if err := c.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, t); err != nil {
			return ToolCallVerdict{Action: ActionBlock, Reason: "on_tool_call error: " + err.Error()}
		}
		ret := c.L.Get(-1)
		c.L.Pop(1)
		if ret == lua.LNil {
			continue // pass to next handler
		}
		tbl, ok := ret.(*lua.LTable)
		if !ok {
			return ToolCallVerdict{Action: ActionBlock, Reason: "on_tool_call: handler must return nil or a table"}
		}
		if v, done := verdictFromTable(tbl, &cmd); done {
			return v
		}
		rewritten = true // {command=...} rewrote cmd; continue the chain.
	}
	// A fall-through with no rewrite is a pure pass (Passthrough). A {command=...}
	// rewrite — even to "" — sets rewritten, so the non-bash gate fails closed on
	// it instead of mistaking the empty-command argv for a pass.
	return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", cmd}, Passthrough: !rewritten}
}

// verdictFromTable interprets a handler's return table. It returns done=true
// with a terminal verdict for block/argv/ask; for {command=...} it updates *cmd
// and returns done=false (continue the chain). A table with none of the known
// keys fails closed.
//
// Keys are checked in a fixed precedence — block > argv > ask > command — so a
// verdict is deterministic even if a handler returns more than one key (an
// unsupported combination). block first means the safe outcome always wins;
// return exactly one key to avoid surprise.
func verdictFromTable(tbl *lua.LTable, cmd *string) (ToolCallVerdict, bool) {
	if b := tbl.RawGetString("block"); b == lua.LTrue {
		return ToolCallVerdict{Action: ActionBlock, Reason: optStr(tbl, "reason")}, true
	}
	if a := tbl.RawGetString("argv"); a != lua.LNil {
		at, ok := a.(*lua.LTable)
		if !ok {
			return ToolCallVerdict{Action: ActionBlock, Reason: "on_tool_call: argv must be a list of strings"}, true
		}
		list, ok := luaStringList(at)
		if !ok {
			return ToolCallVerdict{Action: ActionBlock, Reason: "on_tool_call: argv must be a non-empty list of strings"}, true
		}
		return ToolCallVerdict{Action: ActionRun, Argv: list}, true
	}
	if p := tbl.RawGetString("ask"); p != lua.LNil {
		// Carry the (possibly rewritten) command so the ask-allow path runs exactly
		// what the human approved — not the original pre-rewrite command.
		v := ToolCallVerdict{Action: ActionAsk, Prompt: lua.LVAsString(p), Reason: optStr(tbl, "reason"),
			Argv: []string{"bash", "-c", *cmd}}
		if at := tbl.RawGetString("ask_timeout"); at != lua.LNil {
			v.AskTimeout = time.Duration(lua.LVAsNumber(at)) * time.Second
		}
		return v, true
	}
	if cv := tbl.RawGetString("command"); cv != lua.LNil {
		*cmd = lua.LVAsString(cv)
		return ToolCallVerdict{}, false // rewrite + continue
	}
	return ToolCallVerdict{Action: ActionBlock, Reason: "on_tool_call: table had no recognized verdict key (block/argv/ask/command)"}, true
}

// luaStringList converts a Lua list table to []string. It returns ok=false when
// the table is empty or any element 1..N is not a string (a hole reads as nil,
// which is not a string, so it is rejected). This is fail-closed input
// validation for the on_tool_call argv shape — a map-style table has Len()==0
// and is rejected as empty.
func luaStringList(t *lua.LTable) ([]string, bool) {
	n := t.Len()
	if n == 0 {
		return nil, false
	}
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		s, ok := t.RawGetInt(i).(lua.LString)
		if !ok {
			return nil, false
		}
		out = append(out, string(s))
	}
	return out, true
}

// luaOnToolCall binds shell3.on_tool_call(fn): append fn to the pre-exec handler
// chain. Chainable — multiple calls run in declaration order.
func (c *LoadedConfig) luaOnToolCall(L *lua.LState) int {
	c.onToolCall = append(c.onToolCall, L.CheckFunction(1))
	return 0
}

// luaOnToolResult binds shell3.on_tool_result(fn): append fn to the post-exec
// output-rewrite chain.
func (c *LoadedConfig) luaOnToolResult(L *lua.LState) int {
	c.onToolResult = append(c.onToolResult, L.CheckFunction(1))
	return 0
}

// HasToolCall reports whether any on_tool_call handler was declared.
func (c *LoadedConfig) HasToolCall() bool { return len(c.onToolCall) > 0 }

// HasToolResult reports whether any on_tool_result handler was declared.
func (c *LoadedConfig) HasToolResult() bool { return len(c.onToolResult) > 0 }

// RunToolResult runs the on_tool_result chain, letting handlers rewrite the
// output the model sees. FAILS OPEN — a broken rewriter must never destroy a
// tool's output, so any error or malformed return passes the current output
// through unchanged.
func (c *LoadedConfig) RunToolResult(ctx context.Context, name, argsJSON, output string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	out := output
	for _, fn := range c.onToolResult {
		r := c.L.NewTable()
		c.L.SetField(r, "name", lua.LString(name))
		c.L.SetField(r, "args", lua.LString(argsJSON))
		c.L.SetField(r, "output", lua.LString(out))
		if err := c.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, r); err != nil {
			continue // fail open: keep current output
		}
		ret := c.L.Get(-1)
		c.L.Pop(1)
		if tbl, ok := ret.(*lua.LTable); ok {
			// Only a string output replaces the original. A non-string value
			// (table, bool, function, …) would coerce to "" via LVAsString and
			// silently destroy the tool output — the exact harm fail-open exists
			// to prevent — so a malformed output passes the original through.
			if s, ok := tbl.RawGetString("output").(lua.LString); ok {
				out = string(s)
			}
		}
	}
	return out
}
