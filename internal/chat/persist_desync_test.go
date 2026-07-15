package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// A resumed session seeds InitialMessages that are ALREADY on disk. The first
// post-resume saveHistory must flush only the messages appended since, not
// re-write the seeded history (which would double messages.jsonl on every
// restart, compounding across restarts).
func TestSaveHistory_AfterResume_DoesNotReflushSeed(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	seed := []llm.Message{
		{Role: llm.RoleUser, Content: "earlier question"},
		{Role: llm.RoleAssistant, Content: "earlier answer"},
	}
	for _, m := range seed {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	// Resume: NewSession seeded with the on-disk history.
	sess := NewSession(SessionOpts{StoreID: id, Store: st, InitialMessages: seed})

	// One post-resume turn appends two messages, then flushes.
	sess.messages = append(sess.messages,
		llm.Message{Role: llm.RoleUser, Content: "new question"},
		llm.Message{Role: llm.RoleAssistant, Content: "new answer"},
	)
	saveHistory(st, applog.Noop{}, sess, id)

	got, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("messages on disk after resumed turn: got %d, want 4 (seed must not be re-flushed)", len(got))
	}
}

// /clear replaces history via SetMessages(nil) + SetID(fresh). SetMessages must
// resync the persisted high-water mark, or saveHistory's persistedLen >
// len(messages) guard silently skips every flush and the fresh session never
// persists anything.
func TestSaveHistory_AfterClear_PersistsNewSession(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	oldID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sess := NewSession(SessionOpts{StoreID: oldID, Store: st})
	sess.messages = append(sess.messages,
		llm.Message{Role: llm.RoleUser, Content: "before clear"},
		llm.Message{Role: llm.RoleAssistant, Content: "reply"},
	)
	saveHistory(st, applog.Noop{}, sess, oldID)

	// /clear: wipe history and rotate onto a fresh store session.
	newID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sess.SetMessages(nil)
	sess.SetID(newID)

	sess.messages = append(sess.messages,
		llm.Message{Role: llm.RoleUser, Content: "after clear"},
		llm.Message{Role: llm.RoleAssistant, Content: "fresh reply"},
	)
	saveHistory(st, applog.Noop{}, sess, newID)

	got, err := st.LoadMessages(newID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages persisted after /clear: got %d, want 2", len(got))
	}
}

// /rollback truncates history via SetMessages(shorter). The high-water mark
// must clamp down so messages appended after the rollback still get flushed.
func TestSaveHistory_AfterRollback_FlushesNewMessages(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sess := NewSession(SessionOpts{StoreID: id, Store: st})
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "q2"},
		{Role: llm.RoleAssistant, Content: "a2"},
	}
	sess.messages = append(sess.messages, msgs...)
	saveHistory(st, applog.Noop{}, sess, id)

	// Roll back the last exchange, then run a new turn.
	sess.SetMessages(msgs[:2])
	sess.messages = append(sess.messages,
		llm.Message{Role: llm.RoleUser, Content: "q2 retry"},
		llm.Message{Role: llm.RoleAssistant, Content: "a2 retry"},
	)
	before, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	saveHistory(st, applog.Noop{}, sess, id)
	after, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(after)-len(before) != 2 {
		t.Fatalf("rollback turn flushed %d messages, want 2", len(after)-len(before))
	}
}
