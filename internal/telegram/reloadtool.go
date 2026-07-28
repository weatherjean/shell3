//go:build unix

package telegram

import (
	"context"

	"github.com/weatherjean/shell3/internal/shell3"
)

// registerReloadTool gives the agent a `reload` tool to apply its own edits to
// the config. It records a pending reload and returns immediately; the host
// applies it at end-of-turn (a mid-turn reload would tear down the running turn).
func (b *Bot) registerReloadTool(s *shell3.Session) {
	_ = s.RegisterHostTool(shell3.HostTool{
		Name: "reload",
		Description: "Apply your edits to the config directory by reloading the config. " +
			"Edit the file first (add/modify a model, agent, tool, skill, or cron job), then call this. " +
			"The reload is validated and applied after this turn ends; if the file is invalid the old config keeps running and you'll see the error.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:    b.reloadToolHandler,
	})
}

func (b *Bot) reloadToolHandler(ctx context.Context, argsJSON string) (string, error) {
	if b.reload == nil {
		return "error: reload is not available", nil
	}
	// Written on the SESSION's turn goroutine, read on the bot's — under b.mu
	// like the rest of the mutable wiring rather than leaning on the event
	// channel's happens-before.
	b.mu.Lock()
	b.pendingReload = true
	b.mu.Unlock()
	return "reload scheduled; it will be validated and applied when this turn ends", nil
}

// applyPendingReload runs a deferred reload if one was requested during the turn
// that just finished. Called at end-of-turn (session idle). Pushes the result.
func (b *Bot) applyPendingReload(ctx context.Context) {
	b.mu.Lock()
	pending := b.pendingReload
	b.pendingReload = false
	b.mu.Unlock()
	if !pending {
		return
	}
	b.runReload(ctx)
}

// runReload performs a config reload while holding the bot's turn slot, so no
// user (handleMsg) or wake (dispatchWake) turn can start mid-swap. Background
// jobs no longer block the reload — they keep their old config until they drain.
// Shared by /reload and the reload tool's end-of-turn apply. After the swap, any
// queued wake is drained.
func (b *Bot) runReload(ctx context.Context) {
	if b.reload == nil {
		b.sendReply(ctx, "reload not available")
		return
	}
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		b.sendReply(ctx, "a turn is in flight — try /reload again when it finishes")
		return
	}
	b.turnActive = true
	b.mu.Unlock()
	res, err := b.reload()
	b.mu.Lock()
	b.turnActive = false
	b.mu.Unlock()
	b.sendReply(ctx, shell3.ReloadReplyText(res, err))
	// A wake that arrived during the reload was queued against the held slot;
	// drain it now rather than stranding it until the next event.
	b.startNextWake(ctx)
}
