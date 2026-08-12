//go:build unix

package telegram

import (
	"context"
	"errors"
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
	if got.ChatID != 42 || got.ID != "7" {
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
		From:    &models.User{ID: 42},
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

// withSendRetry retries transient network failures and gives up immediately
// on API rejections and cancelled contexts.
func TestWithSendRetry(t *testing.T) {
	calls := 0
	out, err := withSendRetry(context.Background(), func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("read tcp: connection reset by peer")
		}
		return "sent", nil
	})
	if err != nil || out != "sent" || calls != 3 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
	}

	calls = 0
	_, err = withSendRetry(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("Bad Request: message is too long")
	})
	if err == nil || calls != 1 {
		t.Fatalf("API rejection must not retry: err=%v calls=%d", err, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	_, err = withSendRetry(ctx, func() (string, error) {
		calls++
		return "", errors.New("dial tcp: i/o timeout")
	})
	if err == nil || calls != 1 {
		t.Fatalf("cancelled ctx must stop retries: err=%v calls=%d", err, calls)
	}
}
