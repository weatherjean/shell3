//go:build unix

package telegram

import (
	"context"
)

// consumeCallbacks routes inline-keyboard presses (the /voice menu) for the
// bot's lifetime on its own goroutine (started by Run), independent of any
// turn ctx.
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

// handleCallback dispatches one inline-keyboard press, then acknowledges it.
// A press with unrecognized callback_data is a harmless no-op.
func (b *Bot) handleCallback(ctx context.Context, cb Callback) {
	if cb.ChatID != b.chatID {
		// Unauthorized: drop silently, exactly as handleMsg drops a message from
		// another chat. A /voice menu posted into a shared chat must not be
		// answerable by anyone but the operator — the buttons carry a trivially
		// guessable callback_data, so the chat is the only boundary. Not even
		// acknowledged: an ack is a reply to a stranger.
		return
	}
	if mode, ok := parseVoiceModeData(cb.Data); ok {
		b.handleVoiceCallback(ctx, mode)
	}
	switch cb.Data {
	case threadAskNewData:
		b.resolveThreadAsk(ctx, true)
	case threadAskCancelData:
		b.resolveThreadAsk(ctx, false)
	}
	// Ack every callback (even unrelated keyboards) so the button's spinner stops.
	_ = b.client.AnswerCallback(ctx, cb.ID)
}
