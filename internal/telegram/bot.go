//go:build unix

package telegram

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	approvals *approvalRegistry // Task 6
}

// approvalRegistry stub — replaced by Task 6.
type approvalRegistry struct{}

func newApprovalRegistry() *approvalRegistry { return &approvalRegistry{} }

// NewBot wires a Bot. sess must be the runtime's persistent "telegram" session.
func NewBot(client tgClient, rt *shell3.Runtime, sess *shell3.Session, chatID int64) *Bot {
	return &Bot{
		client:    client,
		rt:        rt,
		sess:      sess,
		chatID:    chatID,
		approvals: newApprovalRegistry(),
	}
}

// Run consumes inbound messages and the wake bus until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.consumeWakes(ctx) // Task 7
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-b.client.Updates(ctx):
			if !ok {
				return
			}
			b.handleMsg(ctx, m)
		}
	}
}

// handleMsg routes one inbound message.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	if m.ChatID != b.chatID {
		return // unauthorized: drop silently
	}
	if m.Callback != nil {
		b.handleCallback(ctx, m.Callback) // Task 6
		return
	}
	if strings.HasPrefix(m.Text, "/") {
		b.handleCommand(ctx, m) // Task 8
		return
	}
	parts := mediaToParts(m.Media) // Task 5
	// HasQueuedInput reports inbox state. In the single-chat v1 flow, handleMsg
	// is serial, so a running turn blocks here until the channel drains.
	// HasQueuedInput catches the case where a wake/cron item is already queued.
	if b.sess.HasQueuedInput() {
		// A turn may be running; Interject never blocks and steers it.
		b.sess.Interject(m.Text, parts...)
		return
	}
	_ = b.client.Typing(ctx, b.chatID)
	ch := b.sess.SendParts(ctx, m.Text, parts)
	reply := drainToReply(ch)
	b.sendReply(ctx, reply)
}

// handleCallback stub — replaced by Task 6.
func (b *Bot) handleCallback(context.Context, *Callback) {}

// handleCommand stub — replaced by Task 8.
func (b *Bot) handleCommand(context.Context, Msg) {}

// mediaToParts stub — replaced by Task 5.
func mediaToParts([]Media) []shell3.Part { return nil }

// consumeWakes stub — replaced by Task 7.
func (b *Bot) consumeWakes(ctx context.Context) {}
