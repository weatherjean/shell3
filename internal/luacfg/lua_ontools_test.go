package luacfg

import (
	"context"
	lua "github.com/yuin/gopher-lua"
	"testing"
)

func loadOnTools(t *testing.T, src string) *LoadedConfig {
	t.Helper()
	L := lua.NewState()
	c := &LoadedConfig{L: L}
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	registerRegex(L)
	L.SetField(tbl, "regex", L.NewFunction(c.luaRegex))
	L.SetField(tbl, "on_tool_call", L.NewFunction(c.luaOnToolCall))
	L.SetField(tbl, "on_tool_result", L.NewFunction(c.luaOnToolResult))
	if err := L.DoString(src); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	return c
}

func TestOnToolCallChainAppends(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t) end)
		shell3.on_tool_call(function(t) end)
	`)
	if len(c.onToolCall) != 2 {
		t.Fatalf("want 2 handlers, got %d", len(c.onToolCall))
	}
	if !c.HasToolCall() {
		t.Fatal("HasToolCall should be true")
	}
}

func TestOnToolResultChainAppends(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_result(function(r) end)`)
	if len(c.onToolResult) != 1 || !c.HasToolResult() {
		t.Fatalf("want 1 result handler, got %d", len(c.onToolResult))
	}
}

func TestRunToolCallBlock(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t)
			if shell3.regex([[rm\s+-rf]]):match(t.command) then
				return { block = true, reason = "no rm -rf" }
			end
		end)`)
	v := c.RunToolCall(context.Background(), "bash", "rm -rf /tmp/x", "{}", false)
	if v.Action != ActionBlock || v.Reason != "no rm -rf" {
		t.Fatalf("want block, got %+v", v)
	}
}

func TestRunToolCallPassRunsDefaultArgv(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) end)`)
	v := c.RunToolCall(context.Background(), "bash", "ls -la", "{}", false)
	if v.Action != ActionRun || len(v.Argv) != 3 || v.Argv[2] != "ls -la" {
		t.Fatalf("want run bash -c 'ls -la', got %+v", v)
	}
	if !v.Passthrough {
		t.Fatalf("a pure pass (no handler opinion) must set Passthrough, got %+v", v)
	}
}

func TestRunToolCallRewriteThenRun(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t) return { command = "echo " .. t.command } end)`)
	v := c.RunToolCall(context.Background(), "bash", "hi", "{}", false)
	if v.Action != ActionRun || v.Argv[2] != "echo hi" {
		t.Fatalf("want rewritten command, got %+v", v)
	}
	if v.Passthrough {
		t.Fatalf("a {command=...} rewrite is not a pass — Passthrough must be false, got %+v", v)
	}
}

// A {command=""} rewrite yields the same empty argv as a pass, but it is a
// command verdict — Passthrough must stay false so the non-bash gate can fail
// closed on it instead of mistaking the argv shape for a pass.
func TestRunToolCallEmptyRewriteNotPassthrough(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) return { command = "" } end)`)
	v := c.RunToolCall(context.Background(), "bash", "hi", "{}", false)
	if v.Action != ActionRun || len(v.Argv) != 3 || v.Argv[2] != "" {
		t.Fatalf("want empty-command run, got %+v", v)
	}
	if v.Passthrough {
		t.Fatalf("an empty {command=...} rewrite must not be Passthrough, got %+v", v)
	}
}

// A terminal {argv=...} verdict is not a pass either — Passthrough stays false.
func TestRunToolCallArgvNotPassthrough(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) return { argv = {"bash","-c",""} } end)`)
	v := c.RunToolCall(context.Background(), "bash", "hi", "{}", false)
	if v.Action != ActionRun || v.Passthrough {
		t.Fatalf("an argv runner-swap must not be Passthrough, got %+v", v)
	}
}

func TestRunToolCallArgvRunnerSwap(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t)
			return { argv = {"docker","exec","c","bash","-c",t.command} }
		end)`)
	v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false)
	if v.Action != ActionRun || v.Argv[0] != "docker" || v.Argv[5] != "ls" {
		t.Fatalf("want runner swap, got %+v", v)
	}
}

func TestRunToolCallAsk(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t) return { ask = "run "..t.command.."?", reason = "denied" } end)`)
	v := c.RunToolCall(context.Background(), "bash", "git push", "{}", false)
	if v.Action != ActionAsk || v.Prompt != "run git push?" || v.Reason != "denied" {
		t.Fatalf("want ask, got %+v", v)
	}
}

func TestRunToolCallHandlerErrorFailsClosed(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) error("boom") end)`)
	v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false)
	if v.Action != ActionBlock {
		t.Fatalf("want fail-closed block, got %+v", v)
	}
}

func TestRunToolCallUnknownKeyFailsClosed(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) return { unknown = true } end)`)
	v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false)
	if v.Action != ActionBlock {
		t.Fatalf("want fail-closed block for unrecognized verdict key, got %+v", v)
	}
}

func TestRunToolCallRewriteThenAskCarriesCommand(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t) return { command = "SAFE " .. t.command } end)
		shell3.on_tool_call(function(t) return { ask = "run " .. t.command .. "?" } end)`)
	v := c.RunToolCall(context.Background(), "bash", "orig", "{}", false)
	if v.Action != ActionAsk {
		t.Fatalf("want ask, got %+v", v)
	}
	if v.Prompt != "run SAFE orig?" {
		t.Fatalf("prompt should show the rewritten command, got %q", v.Prompt)
	}
	if len(v.Argv) != 3 || v.Argv[2] != "SAFE orig" {
		t.Fatalf("ask verdict must carry the rewritten command as argv, got %+v", v.Argv)
	}
}

func TestRunToolResultRewrite(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_result(function(r) return { output = "[redacted]" } end)`)
	if got := c.RunToolResult(context.Background(), "bash", "{}", "secret"); got != "[redacted]" {
		t.Fatalf("want redacted, got %q", got)
	}
}

func TestRunToolResultPassThrough(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_result(function(r) end)`)
	if got := c.RunToolResult(context.Background(), "bash", "{}", "keep"); got != "keep" {
		t.Fatalf("want keep, got %q", got)
	}
}

func TestRunToolResultErrorFailsOpen(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_result(function(r) error("boom") end)`)
	if got := c.RunToolResult(context.Background(), "bash", "{}", "orig"); got != "orig" {
		t.Fatalf("fail-open: want orig, got %q", got)
	}
}

// A handler that returns a non-string output (here a table) must NOT replace the
// real output — coercing it would silently nuke the tool output to "". Fail open.
func TestRunToolResultNonStringOutputFailsOpen(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_result(function(r) return { output = {} } end)`)
	if got := c.RunToolResult(context.Background(), "bash", "{}", "orig"); got != "orig" {
		t.Fatalf("fail-open: a non-string output must pass through, got %q", got)
	}
}

// The first terminal verdict wins; later handlers in the chain must not run.
func TestRunToolCallFirstTerminalShortCircuits(t *testing.T) {
	c := loadOnTools(t, `
		ran_second = false
		shell3.on_tool_call(function(t) return { block = true, reason = "first" } end)
		shell3.on_tool_call(function(t) ran_second = true end)`)
	v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false)
	if v.Action != ActionBlock || v.Reason != "first" {
		t.Fatalf("want first handler's block, got %+v", v)
	}
	if lua.LVAsBool(c.L.GetGlobal("ran_second")) {
		t.Fatal("second handler must not run after a terminal verdict")
	}
}

func TestRunToolCallEmptyArgvFailsClosed(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) return { argv = {} } end)`)
	if v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false); v.Action != ActionBlock {
		t.Fatalf("want fail-closed block for empty argv, got %+v", v)
	}
}

func TestRunToolCallNonStringArgvFailsClosed(t *testing.T) {
	c := loadOnTools(t, `shell3.on_tool_call(function(t) return { argv = {"docker", 5} } end)`)
	if v := c.RunToolCall(context.Background(), "bash", "ls", "{}", false); v.Action != ActionBlock {
		t.Fatalf("want fail-closed block for non-string argv element, got %+v", v)
	}
}

// TestRunToolCallHeadlessField: t.headless mirrors the per-call headless bit
// and is branchable from Lua, for bash and non-bash tools alike.
func TestRunToolCallHeadlessField(t *testing.T) {
	c := loadOnTools(t, `
		shell3.on_tool_call(function(t)
			if type(t.headless) ~= "boolean" then
				return { block = true, reason = "headless missing" }
			end
			if t.headless then
				return { block = true, reason = "headless" }
			end
			return nil
		end)`)
	ctx := context.Background()
	for _, tool := range []struct{ name, command string }{
		{"bash", "echo hi"},
		{"read", ""}, // non-bash: t.command is nil, t.headless must still be set
	} {
		v := c.RunToolCall(ctx, tool.name, tool.command, "{}", true)
		if v.Action != ActionBlock || v.Reason != "headless" {
			t.Fatalf("%s headless=true: want block(headless), got %+v", tool.name, v)
		}
		v = c.RunToolCall(ctx, tool.name, tool.command, "{}", false)
		if v.Action != ActionRun {
			t.Fatalf("%s headless=false: want pass, got %+v", tool.name, v)
		}
	}
}
