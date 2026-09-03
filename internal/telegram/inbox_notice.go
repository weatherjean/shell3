//go:build unix

package telegram

import (
	"context"
	"fmt"
)

// NotifyInbox posts host-owned awareness text to the home chat. It does not
// open a session, enqueue model input, or start an agent turn.
func (b *Bot) NotifyInbox(ctx context.Context, count int) error {
	if count < 1 {
		return nil
	}
	noun := "notices"
	object := "them"
	if count == 1 {
		noun = "notice"
		object = "it"
	}
	_, err := b.client.Send(ctx, b.homeChat,
		fmt.Sprintf("Inbox: %d pending %s. Ask me to check the inbox when you want me to handle %s.", count, noun, object))
	return err
}
