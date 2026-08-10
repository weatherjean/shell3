//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// registerMailTool adds the mail_user host tool to a chat session: the
// agent's one way to reach the user from a quiet turn (background mail, cron
// results). The host prefixes the posted message with ✉️ — bare chat text
// stays reserved for direct replies to the user. The message threads into the
// session's conversation when a thread anchor exists; otherwise it lands as a fresh message the user can reply to
// — postReply records the sent id in the thread index either way, so a reply
// to it continues this session.
func (b *Bot) registerMailTool(sess *shell3.Session) {
	_ = sess.RegisterHostTool(shell3.HostTool{
		Name: "mail_user",
		Description: "Send a message to the user's chat. In a quiet turn (background mail, a cron " +
			"result) this is the ONLY way to reach the user — the turn's reply text is not " +
			"delivered. Threads into the current conversation when one exists, otherwise starts " +
			"a new thread the user can reply to. Mail what the user needs to see; a routine " +
			"result nobody is waiting on needs no mail. Send AT MOST ONE message per " +
			"completion, then end the turn — never repeat a message you already sent.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "The message, verbatim (markdown ok)."},
			},
			"required": []string{"text"},
		},
		Handler: b.mailUserHandler(sess),
	})
}

// mailUserHandler is mail_user's implementation for one session, split out so
// tests can drive it the way a live turn would.
func (b *Bot) mailUserHandler(sess *shell3.Session) func(ctx context.Context, argsJSON string) (string, error) {
	return func(ctx context.Context, argsJSON string) (string, error) {
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &p); err != nil || strings.TrimSpace(p.Text) == "" {
			return "", fmt.Errorf("mail_user needs a non-empty text")
		}
		text := strings.TrimSpace(p.Text)
		// A looping model re-sends the same mail over and over (observed live:
		// four identical sends in one quiet turn). An identical repeat is
		// answered with guidance instead of another chat message — a hard stop
		// the model can't talk itself past.
		b.mu.Lock()
		if b.lastMailed == text {
			b.mu.Unlock()
			return "already mailed exactly this — the user has it. Do not send it again; end the turn.", nil
		}
		b.lastMailed = text
		replyTo := b.mainAnchor
		b.mu.Unlock()
		// The host marks agent mail with ✉️ so bare text in the chat always
		// means a direct reply to the user's own message; under /quiet the
		// send arrives without a ping.
		var opts []SendOpt
		if b.isQuiet() {
			opts = append(opts, SendOpt{Silent: true})
		}
		b.postReply(ctx, sess, replyTo, "✉️ "+text, opts...)
		return "mailed", nil
	}
}
