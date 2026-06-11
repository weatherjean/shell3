//go:build unix

package telegram

import (
	"context"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// registerReloadTool gives the agent a `reload` tool to apply its own edits to
// shell3.lua. It records a pending reload and returns immediately; the host
// applies it at end-of-turn (a mid-turn reload would tear down the running turn).
func (b *Bot) registerReloadTool() {
	_ = b.sess.RegisterHostTool(shell3.HostTool{
		Name: "reload",
		Description: "Apply your edits to shell3.lua by reloading the config. " +
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
	b.pendingReload = true
	return "reload scheduled; it will be validated and applied when this turn ends", nil
}

// applyPendingReload runs a deferred reload if one was requested during the turn
// that just finished. Called at end-of-turn (session idle). Pushes the result.
func (b *Bot) applyPendingReload(ctx context.Context) {
	if !b.pendingReload {
		return
	}
	b.pendingReload = false
	res, err := b.reload()
	if err != nil {
		b.sendReply(ctx, "❌ reload failed: "+err.Error())
		return
	}
	b.sendReply(ctx, formatReload(res))
}
