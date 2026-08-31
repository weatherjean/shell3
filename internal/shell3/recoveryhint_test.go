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

func TestRecoveryHintDoesNotRecommendUnavailableCommand(t *testing.T) {
	hint := RecoveryHint(errors.New("400 Bad Request"))
	if strings.Contains(hint, "/compact") {
		t.Fatalf("RecoveryHint recommends unavailable /compact command: %q", hint)
	}
	if !strings.Contains(hint, "/new") {
		t.Fatalf("RecoveryHint does not name Telegram's supported reset command: %q", hint)
	}
}

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
