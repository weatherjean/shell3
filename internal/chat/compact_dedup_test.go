package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestCompactInto_NoDuplicateMessages verifies that compactInto does NOT
// re-append messages that were already persisted by prior saveHistory calls.
//
// Before the fix, compactInto flushed the FULL sess.messages slice to the
// outgoing session's messages.jsonl. Since saveHistory already appended those
// same messages across prior turns, every message got duplicated in the
// append-only JSONL file.
//
// After the fix, compactInto flushes only sess.messages[persistedLen:] — the
// unsaved tail — so already-persisted messages are never written twice.
func TestCompactInto_NoDuplicateMessages(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}

	// Create the outgoing session and simulate three turns that each called
	// saveHistory: messages 0-2 are already on disk.
	prevID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "turn 1 user"},
		{Role: llm.RoleAssistant, Content: "turn 1 assistant"},
		{Role: llm.RoleUser, Content: "turn 2 user"},
	}

	// Simulate prior saveHistory calls: append all three messages to disk.
	for _, m := range msgs {
		if err := st.AppendMessage(prevID, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	// Build the session as the turn loop would leave it after those turns:
	// sess.messages = all three messages, persistedLen = 3 (all on disk).
	sess := NewSession(SessionOpts{StoreID: prevID})
	sess.messages = append(sess.messages, msgs...)
	sess.persistedLen = len(msgs) // high-water mark: all persisted

	allMsgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		msgs[0],
		msgs[1],
		msgs[2],
	}

	// Call compactInto; it should flush ONLY the unsaved tail (nothing, since
	// persistedLen == len(sess.messages)) to the outgoing session.
	compactInto(CompactSummary{Summary: "compacted"}, st, sess, allMsgs, applog.Noop{}, "", "")

	// The outgoing session (prevID) must contain EXACTLY the 3 original
	// messages — no duplicates.
	got, err := st.LoadMessages(prevID)
	if err != nil {
		t.Fatalf("LoadMessages(prevID): %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("outgoing session: got %d messages, want %d (duplication bug?)", len(got), len(msgs))
		for i, m := range got {
			t.Logf("  [%d] role=%s content=%q", i, m.Role, m.Content)
		}
	}
	for i := range msgs {
		if i >= len(got) {
			break
		}
		if got[i].Role != msgs[i].Role || got[i].Content != msgs[i].Content {
			t.Errorf("outgoing[%d]: got {%s %q}, want {%s %q}",
				i, got[i].Role, got[i].Content, msgs[i].Role, msgs[i].Content)
		}
	}
}
