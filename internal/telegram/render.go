//go:build unix

package telegram

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/mdhtml"
)

const tgMaxMessage = 4096

// drainTurn consumes a turn's event channel and returns the assistant text.
// Channel close is the authoritative end-of-turn signal. The Done event carries
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

// chunk splits s into pieces no longer than tgMaxMessage bytes, preferring
// newline boundaries. Cuts land on rune boundaries — a mid-rune split would
// send Telegram invalid UTF-8, which it rejects with a 400 (losing the chunk).
func chunk(s string) []string {
	const max = tgMaxMessage
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut <= 0 {
			cut = max
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
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
	for _, c := range chunk(text) {
		// Render the agent's Markdown to Telegram-safe HTML so bold/italics/code
		// show up. If Telegram still rejects it, fall back to the raw text.
		html := mdhtml.ToTelegramHTML(c)
		if _, err := b.client.SendHTML(ctx, b.chatID, html); err != nil {
			_, _ = b.client.Send(ctx, b.chatID, c)
		}
	}
}
