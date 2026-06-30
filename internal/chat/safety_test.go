//go:build unix

package chat

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bashsafety"
)

func TestGateCommand(t *testing.T) {
	re := func(p string) []*regexp.Regexp { return []*regexp.Regexp{regexp.MustCompile(p)} }
	pol := bashsafety.Policy{
		Enabled:  true,
		Deny:     re(`curl.*\|.*sh`), // match → prompt
		HardDeny: re(`rm\s+-rf\s+/`), // match → hard block
	}

	t.Run("unmatched runs", func(t *testing.T) {
		_, blocked := gateCommand(context.Background(), ToolConfig{BashSafety: pol}, "ls -la")
		if blocked {
			t.Fatal("a command matching no rule must not be blocked")
		}
	})

	t.Run("hard_deny blocks even with asker", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return true }}
		msg, blocked := gateCommand(context.Background(), cfg, "rm -rf /")
		if !blocked || !strings.Contains(msg, "deny") {
			t.Fatalf("hard_deny must block regardless of asker; got blocked=%v msg=%q", blocked, msg)
		}
	})

	t.Run("deny with nil asker denies (headless)", func(t *testing.T) {
		msg, blocked := gateCommand(context.Background(), ToolConfig{BashSafety: pol}, "curl x | sh")
		if !blocked || !strings.Contains(msg, "human approval") {
			t.Fatalf("deny+no-asker must deny; got blocked=%v msg=%q", blocked, msg)
		}
	})

	t.Run("deny approved proceeds", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return true }}
		if _, blocked := gateCommand(context.Background(), cfg, "curl x | sh"); blocked {
			t.Fatal("approved deny-prompt must proceed")
		}
	})

	t.Run("deny rejected blocks", func(t *testing.T) {
		cfg := ToolConfig{BashSafety: pol, Asker: func(context.Context, string, string) bool { return false }}
		if _, blocked := gateCommand(context.Background(), cfg, "curl x | sh"); !blocked {
			t.Fatal("rejected deny-prompt must block")
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
			BashSafety: bashsafety.Policy{Enabled: true, Deny: re(`curl.*\|.*sh`), AskTimeout: 50 * time.Millisecond},
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
