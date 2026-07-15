package shell3

import (
	"errors"
	"fmt"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestRollbackHint(t *testing.T) {
	yes := []string{
		`llm: stream: POST "https://api.minimax.io/v1/chat/completions": 400 Bad Request {"type":"bad_request_error","message":"invalid params, history message not support audio (2013)","http_code":"400"}`,
		`400 Bad Request`,
	}
	for _, s := range yes {
		if RollbackHint(errors.New(s)) == "" {
			t.Errorf("expected rollback hint for %q", s)
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
		if RollbackHint(errors.New(s)) != "" {
			t.Errorf("did not expect hint for %q", s)
		}
	}
	if RollbackHint(nil) != "" {
		t.Error("nil err should yield no hint")
	}
}

// The adapter wraps provider API errors in llm.StatusError; the hint must key
// off the typed code (regardless of how the provider phrases the message).
func TestRollbackHint_TypedStatusError(t *testing.T) {
	badReq := &llm.StatusError{Code: 400, Err: errors.New("provider-specific phrasing with no recognizable status text")}
	if RollbackHint(fmt.Errorf("wrapped: %w", badReq)) == "" {
		t.Error("expected hint for typed 400")
	}
	unauth := &llm.StatusError{Code: 401, Err: errors.New("nope")}
	if RollbackHint(unauth) != "" {
		t.Error("did not expect hint for typed 401")
	}
}
