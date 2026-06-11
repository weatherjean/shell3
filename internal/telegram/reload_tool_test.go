//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestReloadTool_DefersToEndOfTurn(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	reloads := 0
	b.SetReloader(func() (shell3.ReloadResult, error) {
		reloads++
		return shell3.ReloadResult{Agents: 1}, nil
	})
	out, err := b.reloadToolHandler(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if reloads != 0 {
		t.Fatal("reload tool must not reload inline (would saw off the running turn)")
	}
	if !b.pendingReload {
		t.Fatal("reload tool must set pendingReload")
	}
	if !strings.Contains(out, "scheduled") {
		t.Fatalf("tool should ack scheduling, got %q", out)
	}
	b.applyPendingReload(context.Background())
	if reloads != 1 || b.pendingReload {
		t.Fatalf("end-of-turn should apply once and clear: reloads=%d pending=%v", reloads, b.pendingReload)
	}
}
