//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/strutil"
)

const inboxPreviewRunes = 140

const (
	StartupNotice  = "๑ï shell3 started"
	ShutdownNotice = "๑ï shell3 shutting down"
)

// NotifyLifecycle posts a host-owned adapter lifecycle message to the home
// chat without opening a session or starting an agent turn.
func (b *Bot) NotifyLifecycle(ctx context.Context, text string) error {
	_, err := b.client.Send(ctx, b.homeChat, text)
	return err
}

// NotifyInbox posts host-owned awareness text to the home chat. It does not
// open a session, enqueue model input, or start an agent turn.
func (b *Bot) NotifyInbox(ctx context.Context, count int, event, body string) error {
	if count < 1 {
		return nil
	}
	noun := "notices"
	object := "them"
	if count == 1 {
		noun = "notice"
		object = "it"
	}
	firstLine, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	preview := strings.Join(strings.Fields(firstLine), " ")
	if event = strings.TrimSpace(event); event != "" {
		if preview == "" {
			preview = event
		} else {
			preview = event + " — " + preview
		}
	}
	text := fmt.Sprintf("✉️ Inbox: %d pending %s.", count, noun)
	if preview != "" {
		text += "\nLatest: " + strutil.Truncate(preview, inboxPreviewRunes)
	}
	text += fmt.Sprintf("\nAsk me to check the inbox when you want me to handle %s.", object)
	_, err := b.client.Send(ctx, b.homeChat, text)
	return err
}
