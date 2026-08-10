//go:build unix

// threadask.go holds the thread-choice gate: a bare (non-reply) message does
// not run until the user confirms it really is a NEW conversation. Typing
// without replying is the most common way to accidentally fork context — the
// old session can't see the new message, so "yes please" lands meaningless in
// a fresh session. The gate asks with two inline buttons (🧵 New thread /
// Cancel); no tap within threadAskTimeout runs it as new (today's semantics —
// a message is never dropped), Cancel discards it (the user swipe-replies
// instead). The very first conversation ever skips the ask: there is nothing
// to continue.
package telegram

import (
	"context"
	"time"
)

// threadAskDefaultTimeout is how long the gate waits for a tap before running
// the held mail as a new thread.
const threadAskDefaultTimeout = 60 * time.Second

// Callback data for the two buttons. Carries no per-ask id: a stale tap with
// nothing held is a no-op in resolveThreadAsk.
const (
	threadAskNewData    = "nt|new"
	threadAskCancelData = "nt|cancel"
)

// maybeHoldForThreadAsk intercepts a bare message: when previous
// conversations exist, it posts the ask (first bare message) or joins the
// held batch (subsequent ones) and returns true. Returns false when the
// message should run immediately (no history — nothing to continue).
// Runs on the update loop, like handleMsg.
func (b *Bot) maybeHoldForThreadAsk(ctx context.Context, mail inMail) bool {
	if !b.threads.Any() {
		return false
	}
	b.mu.Lock()
	if b.heldMail != nil {
		b.heldMail = append(b.heldMail, mail)
		b.mu.Unlock()
		return true
	}
	b.heldMail = []inMail{mail}
	timeout := b.threadAskTimeout
	if timeout <= 0 {
		timeout = threadAskDefaultTimeout
	}
	b.heldTimer = time.AfterFunc(timeout, func() {
		b.resolveThreadAsk(context.Background(), true)
	})
	b.mu.Unlock()

	id, err := b.client.SendMenu(ctx, b.chatID,
		"Start a new thread? (To continue a conversation, cancel and reply to one of its messages instead. No tap = new thread in 60s.)",
		[]MenuOption{
			{Label: "🧵 New thread", Data: threadAskNewData},
			{Label: "Cancel", Data: threadAskCancelData},
		})
	if err != nil {
		// Can't ask — run as new rather than sit on the mail.
		b.resolveThreadAsk(ctx, true)
		return true
	}
	b.mu.Lock()
	b.heldMenuID = id
	b.mu.Unlock()
	return true
}

// resolveThreadAsk settles the held batch: newThread runs it (fresh session,
// normal turn path — queueing behind an active turn), cancel drops it. Safe
// to call multiple times / with nothing held (tap + timer race): the first
// caller takes the batch, later ones see nil and return.
func (b *Bot) resolveThreadAsk(ctx context.Context, newThread bool) {
	b.mu.Lock()
	batch := b.heldMail
	menuID := b.heldMenuID
	if b.heldTimer != nil {
		b.heldTimer.Stop()
	}
	b.heldMail, b.heldMenuID, b.heldTimer = nil, "", nil
	b.mu.Unlock()
	if batch == nil {
		return
	}

	if menuID != "" {
		note := "🧵 new thread"
		if !newThread {
			note = "✖️ cancelled — reply to a message to continue that conversation"
		}
		_ = b.client.EditPlain(ctx, b.chatID, menuID, note)
	}
	if !newThread {
		return
	}

	b.mu.Lock()
	if b.turnActive {
		b.mailQueue = append(b.mailQueue, batch...)
		b.mu.Unlock()
		return
	}
	turnCtx, cancel := b.takeSlotLocked(ctx)
	hadVoice := false
	for _, mail := range batch {
		hadVoice = hadVoice || mail.hadVoice
	}
	b.turnHadVoice = hadVoice
	b.mu.Unlock()
	go b.runUserTurn(ctx, turnCtx, cancel, batch)
}
