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
func (c *conversation) applyPendingReload(ctx context.Context) {
	c.b.mu.Lock()
	pending := c.b.pendingReload
	c.b.pendingReload = false
	c.b.mu.Unlock()
	if !pending {
		return
	}
	c.runReload(ctx)
}

// runReload performs a config reload while the reload latch is held, so no
// turn in ANY room can start mid-swap — the rooms share one Parts, so a swap
// is global even though the request came from one chat. It is refused while
// any room is mid-turn rather than racing it. Background jobs no longer block
// the reload: they keep their old config until they drain. Shared by /reload
// and the reload tool's end-of-turn apply. After the swap, work queued in
// every room is drained.
func (c *conversation) runReload(ctx context.Context) {
	if c.b.reload == nil {
		c.sendReply(ctx, "reload not available")
		return
	}
	if !c.b.beginReload() {
		c.sendReply(ctx, "a turn is in flight — try /reload again when it finishes")
		return
	}
	res, err := c.b.reload()
	// Re-read every room's title/description NOW rather than marking the cache
	// stale: a reload is off the turn path, and an operator who just renamed a
	// room expects the next turn to know it.
	c.b.refreshAllChatMeta(ctx)
	c.b.endReload()
	c.sendReply(ctx, shell3.ReloadReplyText(res, err))
	// Work that arrived during the reload was queued against the latch; drain
	// it now, in every room, rather than stranding it until the next event.
	c.b.startNextWorkAll(ctx, c)
}

// beginReload latches the reload: it fails when any room is mid-turn or
// another reload is already running, and otherwise blocks new turns until
// endReload.
func (b *Bot) beginReload() bool {
	if b.anyBusy() {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reloading || b.activeTurns > 0 {
		return false
	}
	b.reloading = true
	return true
}

// endReload releases the reload latch.
func (b *Bot) endReload() {
	b.mu.Lock()
	b.reloading = false
	b.mu.Unlock()
}
