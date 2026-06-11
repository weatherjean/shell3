//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestStatusTool_ReportsAgentAndConfig(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	out, err := b.statusToolHandler(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "agent: code") {
		t.Fatalf("status should report the active agent, got %q", out)
	}
	if !strings.Contains(out, "config:") {
		t.Fatalf("status should report a config line, got %q", out)
	}
	if !strings.Contains(out, "cron: none") {
		t.Fatalf("status should report cron state, got %q", out)
	}
}
