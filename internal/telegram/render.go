//go:build unix

package telegram

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

const tgMaxMessage = 4096

// drainToReply consumes a turn's event channel and returns the assistant text.
// Channel close is the authoritative end-of-turn signal.
func drainToReply(ch <-chan shell3.Event) string {
	var b strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case shell3.Token:
			b.WriteString(ev.Text)
		case shell3.Error:
			if ev.Err != nil {
				b.WriteString("\n⚠️ " + ev.Err.Error())
				if h := shell3.RollbackHint(ev.Err); h != "" {
					b.WriteString("\n💡 " + h)
				}
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// drainTurn is drainToReply plus per-turn usage capture: the Done event carries
// the turn's cumulative token totals, which it reports to onUsage (if set).
func (b *Bot) drainTurn(ch <-chan shell3.Event) string {
	var sb strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case shell3.Token:
			sb.WriteString(ev.Text)
		case shell3.Error:
			if ev.Err != nil {
				sb.WriteString("\n⚠️ " + ev.Err.Error())
				if h := shell3.RollbackHint(ev.Err); h != "" {
					sb.WriteString("\n💡 " + h)
				}
			}
		case shell3.Done:
			if b.onUsage != nil {
				b.onUsage(ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// chunk splits s into pieces no longer than max bytes, preferring newline
// boundaries.
func chunk(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut <= 0 {
			cut = max
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// sendReply posts text to the chat, chunked.
func (b *Bot) sendReply(ctx context.Context, text string) {
	if text == "" {
		text = "(no output)"
	}
	for _, c := range chunk(text, tgMaxMessage) {
		_, _ = b.client.Send(ctx, b.chatID, c, nil)
	}
}
