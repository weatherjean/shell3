package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestSaveHistory_AfterCompaction(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}

	newID, err := st.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	compactedMsgs := []llm.Message{
		{Role: llm.RoleUser, Content: "<system-reminder>Continuation of session old-id...</system-reminder>"},
		{Role: llm.RoleAssistant, Content: "trigger assistant message"},
	}
	for _, m := range compactedMsgs {
		if err := st.AppendMessage(newID, m); err != nil {
			t.Fatalf("AppendMessage (compacted): %v", err)
		}
	}

	sess := NewSession(SessionOpts{StoreID: newID})
	sess.messages = append(sess.messages, compactedMsgs...)
	sess.persistedLen = len(compactedMsgs)

	thisTurnUser := llm.Message{Role: llm.RoleUser, Content: "this turn's user message"}
	thisTurnAssistant := llm.Message{Role: llm.RoleAssistant, Content: "this turn's assistant reply"}
	sess.messages = append(sess.messages, thisTurnUser, thisTurnAssistant)

	saveHistory(st, applog.Noop{}, sess, newID)

	got, err := st.LoadMessages(newID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	want := append(append([]llm.Message{}, compactedMsgs...), thisTurnUser, thisTurnAssistant)
	if len(got) != len(want) {
		t.Errorf("got %d messages, want %d", len(got), len(want))
		for i, m := range got {
			t.Logf("  got[%d] role=%s content=%q", i, m.Role, m.Content)
		}
		for i, m := range want {
			t.Logf("  want[%d] role=%s content=%q", i, m.Role, m.Content)
		}
		t.FailNow()
	}

	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("messages[%d]: got {%s %q}, want {%s %q}",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}

	if sess.persistedLen != 4 {
		t.Errorf("persistedLen: got %d, want 4", sess.persistedLen)
	}
}
