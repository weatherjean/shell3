package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestHandlerStatusDoesNotDependOnOutput(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
	}{
		{output: "error: example printed successfully"},
		{output: "partial output", err: errors.New("failed")},
		{err: errors.New("failed")},
	} {
		got := handlerResult(tc.output, tc.err)
		if got.isError != (tc.err != nil) || !strings.Contains(got.output, tc.output) {
			t.Fatalf("outcome = %+v", got)
		}
		host := dispatchHostTool(context.Background(), TurnConfig{HostTool: func(context.Context, string, string) (string, error) { return tc.output, tc.err }}, "example", "{}")
		if host != got {
			t.Fatalf("host outcome = %+v, want %+v", host, got)
		}
	}
}

func newTestSession(t *testing.T) *Session {
	t.Helper()
	return NewSession(SessionOpts{})
}

func TestCompactInto_KeepsTail(t *testing.T) {
	sess := newTestSession(t)
	sess.messages = []llm.Message{
		msg(llm.RoleUser, "old-1"), msg(llm.RoleAssistant, "old-2"),
		msg(llm.RoleUser, "recent-1"), msg(llm.RoleAssistant, "recent-2"),
	}
	tail := sess.messages[2:]
	compactInto(CompactSummary{Summary: "did stuff"}, nil, sess, tail, applog.Noop{})

	if len(sess.messages) != 3 {
		t.Fatalf("len = %d, want 3 (continuation + 2 tail)", len(sess.messages))
	}
	if sess.messages[0].Role != llm.RoleUser || !strings.Contains(sess.messages[0].Content, "did stuff") {
		t.Fatalf("first message should be the continuation summary, got %+v", sess.messages[0])
	}
	if sess.messages[1].Content != "recent-1" || sess.messages[2].Content != "recent-2" {
		t.Fatalf("tail not preserved verbatim: %+v", sess.messages[1:])
	}
}
