package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

// newTestSession constructs a plain in-memory session (no sink, no store) for
// use in compact tests.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	return NewSession(SessionOpts{})
}

func TestCompactInto_IncludesFilePointerSections(t *testing.T) {
	sess := newTestSession(t)
	sess.messages = []llm.Message{{Role: llm.RoleUser, Content: "old context"}}

	compactInto(CompactSummary{
		Summary:        "summary",
		ImportantFiles: []string{"a.go"},
	}, nil, sess, nil, applog.Noop{}, "", "")
	if len(sess.messages) < 1 {
		t.Fatalf("expected at least a continuation message, got %d", len(sess.messages))
	}

	continuation := sess.messages[0].Content
	for _, want := range []string{
		"<modified-files>", "- a.go", "</modified-files>",
	} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("expected continuation to contain %q, got:\n%s", want, continuation)
		}
	}
}

func TestCompactInto_KeepsTail(t *testing.T) {
	sess := newTestSession(t)
	sess.messages = []llm.Message{
		msg(llm.RoleUser, "old-1"), msg(llm.RoleAssistant, "old-2"),
		msg(llm.RoleUser, "recent-1"), msg(llm.RoleAssistant, "recent-2"),
	}
	tail := sess.messages[2:]
	compactInto(CompactSummary{Summary: "did stuff"}, nil, sess, tail, applog.Noop{}, "", "")

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
