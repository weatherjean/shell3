//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestBotAPIClientSendHTMLReplyOnWire(t *testing.T) {
	var path string
	var payload map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse request: %v", err)
		}
		payload = map[string]string{}
		for key := range r.Form {
			payload[key] = r.Form.Get(key)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":123,"date":0,"chat":{"id":42,"type":"private"}}}`))
	}))
	defer srv.Close()

	b, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	c := &BotAPIClient{b: b}
	id, err := c.SendHTMLReply(context.Background(), 42, "<b>hello</b>", "7", SendOpt{Silent: true})
	if err != nil {
		t.Fatal(err)
	}
	if id != "123" || !strings.HasSuffix(path, "/bottoken/sendMessage") {
		t.Fatalf("id=%q path=%q", id, path)
	}
	if payload["parse_mode"] != "HTML" || payload["disable_notification"] != "true" {
		t.Fatalf("payload = %#v", payload)
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(payload["reply_parameters"]), &reply); err != nil || reply["message_id"] != float64(7) {
		t.Fatalf("reply_parameters = %#v", payload["reply_parameters"])
	}
}

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

func TestNormalizeMessage_TextWinsOverCaption(t *testing.T) {
	m := &models.Message{Chat: models.Chat{ID: 42}, Text: "plain", Caption: "cap"}
	if got := normalizeMessage(m).Text; got != "plain" {
		t.Fatalf("Text = %q, want %q", got, "plain")
	}
}

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
