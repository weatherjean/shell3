//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// A media message carries its words in Caption, not Text: a photo sent with
// "translate this" has Text=="" and Caption=="translate this". Dropping the
// caption loses the whole instruction.
func TestNormalizeMessage_CaptionIsTheText(t *testing.T) {
	m := &models.Message{
		ID:      7,
		Chat:    models.Chat{ID: 42},
		Caption: "translate this into English",
		Photo:   []models.PhotoSize{{FileID: "f1", FileSize: 100}},
	}
	got := normalizeMessage(m)
	if got.Text != "translate this into English" {
		t.Fatalf("Text = %q, want the caption", got.Text)
	}
	if got.ChatID != 42 || got.ID != 7 {
		t.Fatalf("unexpected envelope: %+v", got)
	}
}

// Text wins when both are set (Telegram never sets both, but the fallback must
// not reorder them).
func TestNormalizeMessage_TextWinsOverCaption(t *testing.T) {
	m := &models.Message{Chat: models.Chat{ID: 42}, Text: "plain", Caption: "cap"}
	if got := normalizeMessage(m).Text; got != "plain" {
		t.Fatalf("Text = %q, want %q", got, "plain")
	}
}

// End to end: a captioned photo's words must reach the model's prompt, not just
// the attachment note preflight injects.
func TestCaptionedPhoto_ReachesTurnPrompt(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	m := &models.Message{
		ID:      101,
		Chat:    models.Chat{ID: 42},
		Caption: "translate this into English",
	}
	b.handleMsg(context.Background(), normalizeMessage(m))

	waitFor(t, func() bool { return client.CallCount() > 0 })
	calls := client.CallsSnapshot()
	last := calls[len(calls)-1]
	var prompt string
	for _, msg := range last.Msgs {
		if msg.Role == llm.RoleUser {
			prompt += msg.Content
		}
	}
	if !strings.Contains(prompt, "translate this into English") {
		t.Fatalf("caption must reach the turn prompt, got %q", prompt)
	}
}
