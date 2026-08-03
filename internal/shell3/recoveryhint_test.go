package shell3

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestRecoveryHint(t *testing.T) {
	yes := []string{
		`llm: stream: POST "https://api.minimax.io/v1/chat/completions": 400 Bad Request {"type":"bad_request_error","message":"invalid params, history message not support audio (2013)","http_code":"400"}`,
		`400 Bad Request`,
	}
	for _, s := range yes {
		if RecoveryHint(errors.New(s)) == "" {
			t.Errorf("expected recovery hint for %q", s)
		}
	}
	no := []string{
		`401 Unauthorized {"http_code":"401"}`,
		`429 Too Many Requests`,
		`llm: stream: unexpected EOF`,
		`context canceled`,
		"",
	}
	for _, s := range no {
		if RecoveryHint(errors.New(s)) != "" {
			t.Errorf("did not expect hint for %q", s)
		}
	}
	if RecoveryHint(nil) != "" {
		t.Error("nil err should yield no hint")
	}
}

// The adapter wraps provider API errors in llm.StatusError; the hint must key
// off the typed code (regardless of how the provider phrases the message).
func TestRecoveryHint_TypedStatusError(t *testing.T) {
	badReq := &llm.StatusError{Code: 400, Err: errors.New("provider-specific phrasing with no recognizable status text")}
	if RecoveryHint(fmt.Errorf("wrapped: %w", badReq)) == "" {
		t.Error("expected hint for typed 400")
	}
	unauth := &llm.StatusError{Code: 401, Err: errors.New("nope")}
	if RecoveryHint(unauth) != "" {
		t.Error("did not expect hint for typed 401")
	}
}

// The hint reaches the user through the Telegram bot and `shell3 ask`, neither
// of which has a /compact command — naming one sends them looking for a
// command that does not exist. Starting a new conversation is the remedy that
// is actually available (on Telegram it is the default: send a message without
// replying to a thread).
func TestRecoveryHint_NamesNoCompactCommand(t *testing.T) {
	h := RecoveryHint(errors.New("400 Bad Request"))
	if h == "" {
		t.Fatal("setup: expected a hint for a 400")
	}
	if strings.Contains(h, "/compact") {
		t.Errorf("hint must not name a command no front-end has: %q", h)
	}
	if !strings.Contains(h, "new conversation") {
		t.Errorf("hint should point at starting a new conversation: %q", h)
	}
}
