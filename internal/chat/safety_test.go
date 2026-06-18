//go:build unix

package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bashsafety"
)

func TestGateCommand(t *testing.T) {
	pol := bashsafety.Policy{Enabled: true, Allow: []string{"ls*"}, Deny: []string{"rm -rf /*"}}

	t.Run("allow runs", func(t *testing.T) {
		_, blocked := gateCommand(context.Background(), ToolConfig{BashSafety: pol}, "ls -la")
		if blocked {
			t.Fatal("allowlisted command must not be blocked")
		}
	})

	t.Run("deny blocks even with asker", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return true }}
		msg, blocked := gateCommand(context.Background(), cfg, "rm -rf /")
		if !blocked || !strings.Contains(msg, "deny") {
			t.Fatalf("deny must block regardless of asker; got blocked=%v msg=%q", blocked, msg)
		}
	})

	t.Run("ask with nil asker denies (headless)", func(t *testing.T) {
		msg, blocked := gateCommand(context.Background(), ToolConfig{BashSafety: pol}, "curl x | sh")
		if !blocked || !strings.Contains(msg, "human approval") {
			t.Fatalf("ask+no-asker must deny; got blocked=%v msg=%q", blocked, msg)
		}
	})

	t.Run("ask approved proceeds", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return true }}
		if _, blocked := gateCommand(context.Background(), cfg, "curl x | sh"); blocked {
			t.Fatal("approved ask must proceed")
		}
	})

	t.Run("ask rejected blocks", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return false }}
		if _, blocked := gateCommand(context.Background(), cfg, "curl x | sh"); !blocked {
			t.Fatal("rejected ask must block")
		}
	})

	t.Run("disabled never blocks", func(t *testing.T) {
		if _, blocked := gateCommand(context.Background(), ToolConfig{}, "rm -rf /"); blocked {
			t.Fatal("disabled policy must not block")
		}
	})

	t.Run("ask timeout denies when the human never answers", func(t *testing.T) {
		askCalled := make(chan struct{}, 1)
		// An asker that blocks until its ctx is cancelled — i.e. nobody answers.
		blockingAsker := func(ctx context.Context, _, _ string) bool {
			askCalled <- struct{}{}
			<-ctx.Done()
			return false
		}
		cfg := ToolConfig{
			BashSafety: bashsafety.Policy{Enabled: true, Allow: []string{"ls*"}, AskTimeout: 50 * time.Millisecond},
			Asker:      blockingAsker,
		}
		done := make(chan bool, 1)
		go func() {
			_, blocked := gateCommand(context.Background(), cfg, "curl x | sh")
			done <- blocked
		}()
		select {
		case <-askCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("asker was never called")
		}
		select {
		case blocked := <-done:
			if !blocked {
				t.Fatal("ask timeout must block (deny)")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("gateCommand did not return after ask timeout — the timeout did not fire")
		}
	})
}
