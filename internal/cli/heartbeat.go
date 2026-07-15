package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/heartbeat"
	"github.com/weatherjean/shell3/internal/shell3"
)

// RunDevHeartbeat fires one heartbeat turn the way the Telegram host's ticker
// would — same prompt, same session — rendering it verbosely and closing with
// the suppression verdict (suppressed vs delivered) so a config's checklist
// can be exercised end-to-end from the terminal.
func RunDevHeartbeat(ctx context.Context, w io.Writer, sess *shell3.Session, hb shell3.Heartbeat) error {
	prompt := heartbeat.Prompt(hb)
	fmt.Fprintln(w, devLabel.Render("· heartbeat prompt:"))
	fmt.Fprintln(w, indent(prompt, "    "))
	reply, err := renderDevEventsReply(w, sess.Send(ctx, prompt))
	fmt.Fprintln(w, devMeta.Render(HeartbeatVerdict(reply)))
	return err
}

// HeartbeatVerdict renders what the Telegram host would do with a heartbeat
// turn's reply: stay silent for a bare HEARTBEAT_OK, or deliver the
// token-stripped alert.
func HeartbeatVerdict(reply string) string {
	clean, drop := heartbeat.Strip(reply)
	if drop {
		return "· heartbeat: HEARTBEAT_OK — the bot would suppress this (no message sent)"
	}
	return "· heartbeat: the bot would deliver: " + clean
}

// renderDevEventsReply renders a turn like renderDevEvents and also returns
// the assembled reply text (the concatenated Token stream).
func renderDevEventsReply(w io.Writer, events <-chan shell3.Event) (string, error) {
	var reply strings.Builder
	tap := make(chan shell3.Event)
	done := make(chan error, 1)
	go func() { done <- renderDevEvents(w, tap) }()
	for ev := range events {
		if ev.Kind == shell3.Token {
			reply.WriteString(ev.Text)
		}
		tap <- ev
	}
	close(tap)
	return reply.String(), <-done
}
