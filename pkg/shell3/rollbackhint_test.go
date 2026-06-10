package shell3

import (
	"errors"
	"testing"
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
