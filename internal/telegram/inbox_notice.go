//go:build unix

package telegram

import (
	"context"
)

const (
	StartupNotice  = "๑ï shell3 started"
	ShutdownNotice = "๑ï shell3 shutting down"
)

// NotifyLifecycle posts a host-owned adapter lifecycle message to the home
// chat without opening a session or starting an agent turn.
func (b *Bot) NotifyLifecycle(ctx context.Context, text string) error {
	_, err := b.client.Send(ctx, b.homeChat, text, SendOpt{Silent: true})
	return err
}
