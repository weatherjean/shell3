package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestHandleCompactHistoryIncludesSkillsToReread(t *testing.T) {
	sess := &Session{
		id:       7,
		messages: []llm.Message{{Role: llm.RoleUser, Content: "old context"}},
	}
	allMsgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "old context"},
	}

	newAllMsgs := compactInto(CompactSummary{
		Summary: "summary",
		Skills:  []string{"writing-plans", "/tmp/codebase-discovery.md"},
	}, nil, sess, allMsgs, applog.Noop{}, "", "", "")
	if len(newAllMsgs) < 2 {
		t.Fatalf("expected system and continuation messages, got %d", len(newAllMsgs))
	}

	continuation := newAllMsgs[1].Content
	for _, want := range []string{
		"<skills-to-reread>",
		"- writing-plans",
		"- /tmp/codebase-discovery.md",
		"</skills-to-reread>",
	} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("expected continuation to contain %q, got:\n%s", want, continuation)
		}
	}
}
