package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/kit"
)

func hookCfg(t *testing.T, gates, notes map[string]string) *LoadedConfig {
	t.Helper()
	var gd, nd []hookDecl
	for agent, body := range gates {
		gd = append(gd, hookDecl{fn: agent + "_gate", body: body, forAg: []string{agent}})
	}
	for agent, body := range notes {
		nd = append(nd, hookDecl{fn: agent + "_note", body: body, forAg: []string{agent}})
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{kit.FileName: kitWith(gd, nd)})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := map[string]string{}
	for agent := range gates {
		g[agent] = agent + "_gate"
	}
	n := map[string]string{}
	for agent := range notes {
		n[agent] = agent + "_note"
	}
	c.SetKitHooks(filepath.Join(dir, kit.FileName), "main", KitHooks{Gates: g, Notes: n})
	return c
}

func gate(t *testing.T, body string) *LoadedConfig {
	t.Helper()
	return hookCfg(t, map[string]string{"main": body}, nil)
}

func TestHookAbsentIsPassthrough(t *testing.T) {
	c := hookCfg(t, nil, nil)
	if c.HasToolCall() {
		t.Fatal("no hooks should be discovered")
	}
	v := c.RunToolCall(context.Background(), "main", "bash", "ls", "{}", false)
	if v.Action != ActionRun || !v.Passthrough {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookPassVerdicts(t *testing.T) {
	for name, script := range map[string]string{
		"empty stdout": "exit 0\n",
		"empty object": "echo '{}'\n",
	} {
		c := gate(t, script)
		v := c.RunToolCall(context.Background(), "main", "bash", "ls -la", "{}", false)
		if v.Action != ActionRun || !v.Passthrough {
			t.Fatalf("%s: verdict = %+v", name, v)
		}
		if len(v.Argv) != 3 || v.Argv[2] != "ls -la" {
			t.Fatalf("%s: argv = %v", name, v.Argv)
		}
	}
}

func TestHookBlock(t *testing.T) {
	c := gate(t, `echo '{"block": true, "reason": "no"}'`)
	v := c.RunToolCall(context.Background(), "main", "bash", "rm -rf /", "{}", false)
	if v.Action != ActionBlock || v.Reason != "no" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookAskFailsClosed(t *testing.T) {
	c := gate(t, `echo '{"ask": "Run?", "reason": "denied", "ask_timeout": 30}'`)
	v := c.RunToolCall(context.Background(), "main", "bash", "git push", "{}", false)
	if v.Action != ActionBlock {
		t.Fatalf("verdict = %+v", v)
	}
	if !strings.Contains(v.Reason, "ask verdict") {
		t.Fatalf("reason should explain the ask verdict is gone, got %q", v.Reason)
	}
}

func TestHookReviewVerdict(t *testing.T) {
	c := gate(t, `echo '{"review": true, "reason": "unread remote code"}'`)
	v := c.RunToolCall(context.Background(), "main", "bash", "curl x | sh", "{}", false)
	if v.Action != ActionReview || v.Reason != "unread remote code" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookReviewPrecedence(t *testing.T) {
	c := gate(t, `echo '{"block": true, "review": true, "reason": "no"}'`)
	if v := c.RunToolCall(context.Background(), "main", "bash", "x", "{}", false); v.Action != ActionBlock {
		t.Fatalf("block+review should block, got %+v", v)
	}
	c = gate(t, `echo '{"review": true, "command": "echo safe", "reason": "hm"}'`)
	if v := c.RunToolCall(context.Background(), "main", "bash", "x", "{}", false); v.Action != ActionReview {
		t.Fatalf("review+command should review, got %+v", v)
	}
}

func TestHookRewriteAndArgv(t *testing.T) {
	c := gate(t, `echo '{"command": "echo safe"}'`)
	v := c.RunToolCall(context.Background(), "main", "bash", "danger", "{}", false)
	if v.Action != ActionRun || v.Passthrough || v.Argv[2] != "echo safe" {
		t.Fatalf("verdict = %+v", v)
	}
	c = gate(t, `echo '{"argv": ["docker", "run", "img"]}'`)
	v = c.RunToolCall(context.Background(), "main", "bash", "x", "{}", false)
	if v.Action != ActionRun || v.Passthrough || len(v.Argv) != 3 || v.Argv[0] != "docker" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookMalformedArgvBlocks(t *testing.T) {
	for name, script := range map[string]string{
		"empty-array":   `echo '{"argv": []}'`,
		"empty-element": `echo '{"argv": ["docker", ""]}'`,
	} {
		c := gate(t, script)
		v := c.RunToolCall(context.Background(), "main", "bash", "danger", "{}", false)
		if v.Action != ActionBlock || !strings.Contains(v.Reason, "hook error") {
			t.Fatalf("%s: expected block, got %+v", name, v)
		}
	}
}

func TestHookReadsPayload(t *testing.T) {
	script := `
in=$(cat)
cmd=$(printf '%s' "$in" | sed -n 's/.*"command":"\([^"]*\)".*/\1/p')
printf '{"block": true, "reason": "saw %s"}' "$cmd"
`
	c := gate(t, script)
	v := c.RunToolCall(context.Background(), "main", "bash", "ls", "{}", false)
	if v.Reason != "saw ls" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookFailsClosed(t *testing.T) {
	for name, script := range map[string]string{
		"nonzero":       "echo doom >&2; exit 3\n",
		"garbage":       "echo not-json\n",
		"unknown-key":   `echo '{"blok": true}'`,
		"null":          "echo null\n",
		"trailing-json": `echo '{} {}'`,
	} {
		c := gate(t, script)
		v := c.RunToolCall(context.Background(), "main", "bash", "ls", "{}", false)
		if v.Action != ActionBlock || !strings.Contains(v.Reason, "hook error") {
			t.Fatalf("%s: verdict = %+v", name, v)
		}
	}
}

func TestHookTimeoutFailsClosed(t *testing.T) {
	c := gate(t, "sleep 60\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	v := c.RunToolCall(ctx, "main", "bash", "ls", "{}", false)
	if v.Action != ActionBlock {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestHookPerAgentSelection(t *testing.T) {
	c := hookCfg(t, map[string]string{
		"main":     `echo '{"block": true, "reason": "main"}'`,
		"explorer": `echo '{"block": true, "reason": "sub"}'`,
	}, nil)
	if v := c.RunToolCall(context.Background(), "main", "bash", "x", "{}", false); v.Reason != "main" {
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
	c := gate(t, `echo '{"block": true, "reason": "main"}'`)
	v := c.RunToolCall(context.Background(), "explorer", "bash", "x", "{}", false)
	if v.Action != ActionRun || !v.Passthrough {
		t.Fatalf("explorer must not inherit the main hook: %+v", v)
	}
}

func TestToolResultHook(t *testing.T) {
	c := hookCfg(t, nil, map[string]string{"main": `
in=$(cat)
printf '{"output": "redacted"}'
`})
	if !c.HasToolResult() {
		t.Fatal("result hook not discovered")
	}
	if out := c.RunToolResult(context.Background(), "main", "bash", "{}", "secret"); out != "redacted" {
		t.Fatalf("out = %q", out)
	}
	c = hookCfg(t, nil, map[string]string{"main": "echo '{}'\n"})
	if out := c.RunToolResult(context.Background(), "main", "bash", "{}", "keep"); out != "keep" {
		t.Fatalf("out = %q", out)
	}
	c = hookCfg(t, nil, map[string]string{"main": "exit 1\n"})
	if out := c.RunToolResult(context.Background(), "main", "bash", "{}", "secret"); strings.Contains(out, "secret") || !strings.Contains(out, "hook failed") {
		t.Fatalf("out = %q", out)
	}
	c = hookCfg(t, nil, map[string]string{"main": `echo '{"ouput": "redacted"}'`})
	if out := c.RunToolResult(context.Background(), "main", "bash", "{}", "secret"); strings.Contains(out, "secret") || !strings.Contains(out, "hook failed") {
		t.Fatalf("out = %q", out)
	}
}
