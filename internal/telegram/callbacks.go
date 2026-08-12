//go:build unix

package telegram

import (
	"context"
)

// consumeCallbacks routes inline-keyboard presses for the bot's lifetime on
// its own goroutine (started by Run), independent of any turn ctx.
func (b *Bot) consumeCallbacks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cb, ok := <-b.client.Callbacks(ctx):
			if !ok {
				return
			}
			b.handleCallback(ctx, cb)
		}
	}
}

func (b *Bot) handleCallback(ctx context.Context, cb Callback) {
	if cb.ChatID != b.chatID {
		return
	}
	// Ack every callback (even unrelated keyboards) so the button's spinner stops.
	_ = b.client.AnswerCallback(ctx, cb.ID)
}
