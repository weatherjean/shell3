package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// The session rolled by compaction must carry the same metadata as any fresh
// session — including the model. Dropping it makes compaction-rolled runs
// show no model in the dashboard and store.
func TestCompactInto_NewSessionKeepsModelMeta(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{Model: "test-model"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sess := NewSession(SessionOpts{StoreID: id, Store: st})
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "q"},
		{Role: llm.RoleAssistant, Content: "a"},
	}

	ok := compactInto(CompactSummary{Summary: "sum"}, st, sess,
		sess.messages[1:], applog.Noop{}, t.TempDir(), "/cfg/shell3.lua", "test-model")
	if !ok {
		t.Fatalf("compactInto failed")
	}

	metas, err := st.ListSessions(10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, m := range metas {
		if m.ID == sess.ID() {
			if m.Model != "test-model" {
				t.Fatalf("rolled session Model = %q, want %q", m.Model, "test-model")
			}
			return
		}
	}
	t.Fatalf("rolled session %s not found in store", sess.ID())
}
