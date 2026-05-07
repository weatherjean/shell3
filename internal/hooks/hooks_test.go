package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestHookAllow(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	_ = os.WriteFile(script,[]byte("#!/bin/bash\necho '{\"action\":\"allow\"}'"), 0755)

	r := hooks.NewRunner(hooks.Config{OnToolCall: hooks.HookEntry{Command: script}})
	allowed, reason, err := r.OnToolCall(context.Background(), "bash", map[string]any{"command": "ls"})
	if err != nil || !allowed || reason != "" {
		t.Errorf("expected allow, got allowed=%v reason=%q err=%v", allowed, reason, err)
	}
}

func TestHookBlock(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	_ = os.WriteFile(script,[]byte("#!/bin/bash\necho '{\"action\":\"block\",\"reason\":\"not allowed\"}'"), 0755)

	r := hooks.NewRunner(hooks.Config{OnToolCall: hooks.HookEntry{Command: script}})
	allowed, reason, err := r.OnToolCall(context.Background(), "bash", map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Errorf("clean denial should not return err, got: %v", err)
	}
	if allowed {
		t.Errorf("expected block, got allowed=%v", allowed)
	}
	if reason != "not allowed" {
		t.Errorf("expected reason=%q, got %q", "not allowed", reason)
	}
}

func TestContextBuildHook(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	_ = os.WriteFile(script,[]byte(`#!/bin/bash
cat | python3 -c "import sys,json; d=json.load(sys.stdin); d['messages']=d['messages'][-1:]; print(json.dumps(d))"
`), 0755)

	r := hooks.NewRunner(hooks.Config{OnContextBuild: hooks.HookEntry{Command: script}})
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleUser, Content: "second"},
	}
	out, err := r.OnContextBuild(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Content != "second" {
		t.Errorf("expected 1 message 'second', got %+v", out)
	}
}

func TestNoHook(t *testing.T) {
	r := hooks.NewRunner(hooks.Config{})
	allowed, reason, err := r.OnToolCall(context.Background(), "bash", nil)
	if err != nil || !allowed || reason != "" {
		t.Errorf("no hook should default to allow: allowed=%v reason=%q err=%v", allowed, reason, err)
	}
}

func TestHookTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir, err := os.MkdirTemp(home, ".shell3-hook-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	script := filepath.Join(dir, "hook.sh")
	_ = os.WriteFile(script, []byte("#!/bin/bash\necho '{\"action\":\"allow\"}'"), 0755)

	rel := "~/" + filepath.Base(dir) + "/hook.sh"
	r := hooks.NewRunner(hooks.Config{OnToolCall: hooks.HookEntry{Command: "bash " + rel}})
	allowed, _, err := r.OnToolCall(context.Background(), "bash", nil)
	if err != nil || !allowed {
		t.Errorf("tilde expansion failed: allowed=%v err=%v", allowed, err)
	}
}
