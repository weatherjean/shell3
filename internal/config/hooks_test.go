package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

// hookCfg loads a minimal tree with the given hooks/ scripts plus an
// explorer subagent (so per-subagent hooks resolve).
func hookCfg(t *testing.T, scripts map[string]string) *LoadedConfig {
	t.Helper()
	extra := map[string]string{
		"agents/explorer.md": "---\ndescription: explores\ntools: [bash]\n---\nExplore.\n",
	}
	for name, body := range scripts {
		extra["hooks/"+name] = body
	}
	return mustLoad(t, extra)
}

func TestHookAbsentIsPassthrough(t *testing.T) {
	c := hookCfg(t, nil)
	if c.HasToolCall() {
		t.Fatal("no hooks should be discovered")
	}
	v := c.RunToolCall(context.Background(), "agent", "bash", "ls", "{}", false)
	if v.Action != ActionRun || !v.Passthrough {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookPassVerdicts(t *testing.T) {
	for name, script := range map[string]string{
		"empty stdout": "exit 0\n",
		"empty object": "echo '{}'\n",
	} {
		c := hookCfg(t, map[string]string{"tool-call.sh": script})
		v := c.RunToolCall(context.Background(), "agent", "bash", "ls -la", "{}", false)
		if v.Action != ActionRun || !v.Passthrough {
			t.Fatalf("%s: verdict = %+v", name, v)
		}
		if len(v.Argv) != 3 || v.Argv[2] != "ls -la" {
			t.Fatalf("%s: argv = %v", name, v.Argv)
		}
	}
}

func TestHookBlock(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-call.sh": `echo '{"block": true, "reason": "no"}'`})
	v := c.RunToolCall(context.Background(), "agent", "bash", "rm -rf /", "{}", false)
	if v.Action != ActionBlock || v.Reason != "no" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookAsk(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-call.sh": `echo '{"ask": "Run?", "reason": "denied", "ask_timeout": 30}'`})
	v := c.RunToolCall(context.Background(), "agent", "bash", "git push", "{}", false)
	if v.Action != ActionAsk || v.Prompt != "Run?" || v.Reason != "denied" {
		t.Fatalf("verdict = %+v", v)
	}
	if v.AskTimeout != 30*time.Second {
		t.Fatalf("ask_timeout = %v", v.AskTimeout)
	}
	if v.Argv[2] != "git push" {
		t.Fatalf("argv = %v", v.Argv)
	}
}

func TestHookRewriteAndArgv(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-call.sh": `echo '{"command": "echo safe"}'`})
	v := c.RunToolCall(context.Background(), "agent", "bash", "danger", "{}", false)
	if v.Action != ActionRun || v.Passthrough || v.Argv[2] != "echo safe" {
		t.Fatalf("verdict = %+v", v)
	}
	c = hookCfg(t, map[string]string{"tool-call.sh": `echo '{"argv": ["docker", "run", "img"]}'`})
	v = c.RunToolCall(context.Background(), "agent", "bash", "x", "{}", false)
	if v.Action != ActionRun || v.Passthrough || len(v.Argv) != 3 || v.Argv[0] != "docker" {
		t.Fatalf("verdict = %+v", v)
	}
}

// A present-but-malformed argv (empty array, or an empty element) fails
// closed — it must block, never fall through to run the command unwrapped.
func TestHookMalformedArgvBlocks(t *testing.T) {
	for name, script := range map[string]string{
		"empty-array":   `echo '{"argv": []}'`,
		"empty-element": `echo '{"argv": ["docker", ""]}'`,
	} {
		c := hookCfg(t, map[string]string{"tool-call.sh": script})
		v := c.RunToolCall(context.Background(), "agent", "bash", "danger", "{}", false)
		if v.Action != ActionBlock || !strings.Contains(v.Reason, "hook error") {
			t.Fatalf("%s: expected block, got %+v", name, v)
		}
	}
}

func TestHookReadsPayload(t *testing.T) {
	// The script blocks with the command it saw — round-trips stdin JSON.
	script := `
in=$(cat)
cmd=$(printf '%s' "$in" | sed -n 's/.*"command":"\([^"]*\)".*/\1/p')
printf '{"block": true, "reason": "saw %s"}' "$cmd"
`
	c := hookCfg(t, map[string]string{"tool-call.sh": script})
	v := c.RunToolCall(context.Background(), "agent", "bash", "ls", "{}", false)
	if v.Reason != "saw ls" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookFailsClosed(t *testing.T) {
	for name, script := range map[string]string{
		"nonzero": "echo doom >&2; exit 3\n",
		"garbage": "echo not-json\n",
	} {
		c := hookCfg(t, map[string]string{"tool-call.sh": script})
		v := c.RunToolCall(context.Background(), "agent", "bash", "ls", "{}", false)
		if v.Action != ActionBlock || !strings.Contains(v.Reason, "hook error") {
			t.Fatalf("%s: verdict = %+v", name, v)
		}
	}
}

func TestHookTimeoutFailsClosed(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-call.sh": "sleep 60\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	v := c.RunToolCall(ctx, "agent", "bash", "ls", "{}", false)
	if v.Action != ActionBlock {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookPerAgentSelection(t *testing.T) {
	c := hookCfg(t, map[string]string{
		"tool-call.sh":          `echo '{"block": true, "reason": "main"}'`,
		"explorer.tool-call.sh": `echo '{"block": true, "reason": "sub"}'`,
	})
	if v := c.RunToolCall(context.Background(), "agent", "bash", "x", "{}", false); v.Reason != "main" {
		t.Fatalf("main verdict = %+v", v)
	}
	if v := c.RunToolCall(context.Background(), "", "bash", "x", "{}", false); v.Reason != "main" {
		t.Fatalf("empty-name verdict = %+v", v)
	}
	if v := c.RunToolCall(context.Background(), "explorer", "bash", "x", "{}", false); v.Reason != "sub" {
		t.Fatalf("sub verdict = %+v", v)
	}
}

func TestHookSubagentWithoutHookIsUngated(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-call.sh": `echo '{"block": true, "reason": "main"}'`})
	v := c.RunToolCall(context.Background(), "explorer", "bash", "x", "{}", false)
	if v.Action != ActionRun || !v.Passthrough {
		t.Fatalf("explorer must not inherit the main hook: %+v", v)
	}
}

func TestHookOrphanWarns(t *testing.T) {
	c := hookCfg(t, map[string]string{"ghost.tool-call.sh": "exit 0\n"})
	found := false
	for _, w := range c.Warnings() {
		if strings.Contains(w, "ghost") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

func TestToolResultHook(t *testing.T) {
	c := hookCfg(t, map[string]string{"tool-result.sh": `
in=$(cat)
printf '{"output": "redacted"}'
`})
	if !c.HasToolResult() {
		t.Fatal("result hook not discovered")
	}
	if out := c.RunToolResult(context.Background(), "agent", "bash", "{}", "secret"); out != "redacted" {
		t.Fatalf("out = %q", out)
	}
	// Pass-through: {}.
	c = hookCfg(t, map[string]string{"tool-result.sh": "echo '{}'\n"})
	if out := c.RunToolResult(context.Background(), "agent", "bash", "{}", "keep"); out != "keep" {
		t.Fatalf("out = %q", out)
	}
	// Failure never passes the output through.
	c = hookCfg(t, map[string]string{"tool-result.sh": "exit 1\n"})
	if out := c.RunToolResult(context.Background(), "agent", "bash", "{}", "secret"); strings.Contains(out, "secret") || !strings.Contains(out, "hook failed") {
		t.Fatalf("out = %q", out)
	}
}
