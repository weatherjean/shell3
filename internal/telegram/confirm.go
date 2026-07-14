//go:build unix

package telegram

import (
	"context"
	"strconv"
	"strings"
)

// confirmPrefix tags the callback_data of an on_tool_call approval button so the
// callback handler can tell it apart from any other inline keyboard.
const confirmPrefix = "bs"

// Ask presents an on_tool_call approval request as an inline keyboard with
// Allow/Deny buttons and blocks until the user taps one (or ctx is cancelled).
// It returns true to allow. It is wired as the session's Asker, so it runs on
// the turn goroutine while Run keeps consuming callbacks on its own goroutine.
// Fail-safe: any send error or a cancelled turn returns false (deny).
func (b *Bot) Ask(ctx context.Context, command, _ string) bool {
	b.askMu.Lock()
	b.askSeq++
	id := strconv.Itoa(b.askSeq)
	ch := make(chan bool, 1)
	b.pending[id] = ch
	b.askMu.Unlock()
	defer func() {
		b.askMu.Lock()
		delete(b.pending, id)
		b.askMu.Unlock()
	}()

	text := "⚠️ command gate needs approval to run:\n" + truncate(command, 600)
	msgID, err := b.client.SendConfirm(ctx, b.chatID, text, confirmData(id, true), confirmData(id, false))
	if err != nil {
		return false
	}

	select {
	case <-ctx.Done():
		// Turn cancelled (e.g. /stop) before an answer — clear the buttons with a
		// detached context since ctx is already done.
		_ = b.client.EditPlain(context.Background(), b.chatID, msgID, "⌛ approval cancelled:\n"+truncate(command, 600))
		return false
	case yes := <-ch:
		outcome := "🚫 denied: " + truncate(command, 600)
		if yes {
			outcome = "✅ allowed: " + truncate(command, 600)
		}
		_ = b.client.EditPlain(ctx, b.chatID, msgID, outcome)
		return yes
	}
}

// consumeCallbacks routes inline-keyboard presses to the waiting Ask. Runs for
// the bot's lifetime on its own goroutine (started by Run), independent of any
// turn ctx, so a button press is delivered even while a turn goroutine blocks
// inside Ask.
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

// handleCallback signals the matching pending Ask, then acknowledges the press.
// The Ask itself edits the message (it holds the command + message id). Signal
// before the AnswerCallback network round-trip so the waiting turn unblocks
// immediately and this (single-threaded) drain loop isn't gated on Telegram
// latency. A press for an unknown/answered id is a harmless no-op.
func (b *Bot) handleCallback(ctx context.Context, cb Callback) {
	id, yes, ok := parseConfirmData(cb.Data)
	if ok {
		b.askMu.Lock()
		ch := b.pending[id]
		b.askMu.Unlock()
		if ch != nil {
			select {
			case ch <- yes: // buffered(1)+default: a double-tap is dropped, never panics
			default: // already answered; ignore a double-tap
			}
		}
	}
	// Ack every callback (even unrelated keyboards) so the button's spinner stops.
	_ = b.client.AnswerCallback(ctx, cb.ID)
}

// confirmData builds the callback_data for an Allow/Deny button: "bs:<id>:y" or
// "bs:<id>:n". Kept well under Telegram's 64-byte callback_data limit.
func confirmData(id string, yes bool) string {
	if yes {
		return confirmPrefix + ":" + id + ":y"
	}
	return confirmPrefix + ":" + id + ":n"
}

// parseConfirmData decodes a confirm button's callback_data. ok is false for any
// data not produced by confirmData (so unrelated keyboards are ignored).
func parseConfirmData(data string) (id string, yes bool, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != confirmPrefix {
		return "", false, false
	}
	switch parts[2] {
	case "y":
		return parts[1], true, true
	case "n":
		return parts[1], false, true
	default:
		return "", false, false
	}
}
