package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestSaveHistory_AfterCompaction verifies that saveHistory correctly persists
// this turn's new user message and assistant reply to the NEW session's
// messages.jsonl even when maybeCompact has already run and reset sess.messages
// to a short continuation (persistedLen=2, len(messages)=2 post-compaction).
//
// The bug: the old saveHistory used a stale `from` parameter captured before
// compaction (e.g. from=10). After compaction, len(sess.messages) is ~2, so
// `from > len(sess.messages)` triggered the early-return bail — and the new
// user message + assistant reply appended AFTER compaction were never written
// to disk (DATA LOSS on a compacting turn).
//
// The fix: saveHistory uses sess.persistedLen (the high-water mark of what's
// actually on disk) instead of the stale `from` parameter.
//
// This test reproduces the seam directly: it sets up a session with
// persistedLen/messages mimicking post-compaction state (new sess.id,
// persistedLen=2, 2 compacted messages already flushed), then appends this
// turn's user message and assistant reply, calls saveHistory, and asserts that
// LoadMessages for the new session contains those two new messages in addition
// to the compacted ones.
func TestSaveHistory_AfterCompaction(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}

	// Simulate the NEW session created by compactInto.
	newID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	// The two compacted messages that compactInto already wrote to disk.
	compactedMsgs := []llm.Message{
		{Role: llm.RoleUser, Content: "<system-reminder>Continuation of session old-id...</system-reminder>"},
		{Role: llm.RoleAssistant, Content: "trigger assistant message"},
	}
	// compactInto flushed these directly; simulate that.
	for _, m := range compactedMsgs {
		if err := st.AppendMessage(newID, m); err != nil {
			t.Fatalf("AppendMessage (compacted): %v", err)
		}
	}

	// Build sess as RunTurn sees it after maybeCompact returns:
	// - new session id
	// - messages = the two compacted messages
	// - persistedLen = 2 (both already on disk)
	sess := NewSession(SessionOpts{StoreID: newID})
	sess.messages = append(sess.messages, compactedMsgs...)
	sess.persistedLen = len(compactedMsgs) // 2

	// Now the turn appends this turn's user message and assistant reply
	// (RunTurn does this after maybeCompact returns).
	thisTurnUser := llm.Message{Role: llm.RoleUser, Content: "this turn's user message"}
	thisTurnAssistant := llm.Message{Role: llm.RoleAssistant, Content: "this turn's assistant reply"}
	sess.messages = append(sess.messages, thisTurnUser, thisTurnAssistant)

	// Call saveHistory — the function under test.
	// With the old `from`-based code we would pass e.g. from=10 (stale),
	// which would trigger `from > len(sess.messages)` (10 > 4) → bail → DATA LOSS.
	// With the fix, saveHistory uses sess.persistedLen=2, so it flushes [2:] = the two new messages.
	saveHistory(st, applog.Noop{}, sess, newID)

	// Assert that LoadMessages contains all 4 messages: 2 compacted + 2 new.
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

	// Also verify persistedLen was advanced to 4.
	if sess.persistedLen != 4 {
		t.Errorf("persistedLen: got %d, want 4", sess.persistedLen)
	}
}
