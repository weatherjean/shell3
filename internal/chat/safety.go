package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/bashsafety"
)

// AskFunc asks a human to approve command (reason explains why it was gated).
// Front-ends supply it (TUI prompt, Telegram buttons). Nil means no human is
// attached (headless subagent) — bash_safety then denies instead of asking.
type AskFunc func(ctx context.Context, command, reason string) bool

// gateCommand applies cfg.BashSafety to command. It returns (message, true) when
// the command must be blocked — the message is the tool result the model sees —
// and ("", false) when the command may run. Order: deny → hard block; allow →
// run; otherwise ask the human via cfg.Asker, and with no asker (headless),
// block with an instructive reason the parent can act on.
func gateCommand(ctx context.Context, cfg ToolConfig, command string) (string, bool) {
	verdict, reason := cfg.BashSafety.Decide(command)
	switch verdict {
	case bashsafety.Run:
		return "", false
	case bashsafety.Deny:
		return "error: blocked by bash_safety (" + reason + ")", true
	case bashsafety.Ask:
		if cfg.Asker != nil {
			// Bound the wait for a human so an unanswered approval (e.g. a Telegram
			// prompt nobody taps) can't park the turn goroutine forever. Applied
			// here, centrally, so it covers every asker (TUI, Telegram). On timeout
			// the asker's ctx is cancelled and it returns false (deny) — fail-safe.
			askCtx := ctx
			if d := cfg.BashSafety.AskTimeout; d > 0 {
				var cancel context.CancelFunc
				askCtx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
			if cfg.Asker(askCtx, command, reason) {
				return "", false
			}
		}
		// No asker (headless subagent) or the human declined. Deny with a reason
		// that tells the model to escalate to the human — for a subagent this
		// result is reported up the inbox to the parent, where a human is attached.
		return "error: blocked by bash_safety — needs human approval (" + reason +
			"). Stop and ask the human before running this.", true
	default:
		return "error: blocked by bash_safety", true
	}
}
