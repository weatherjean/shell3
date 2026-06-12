package shell3

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/store"
)

// fakeCfgWithStore mirrors fakeCfg but wires a shared on-disk Store so turns
// persist their message stream. ContextWindow is set because newSession's
// ContextWindowFor closure (and the turn's reminder accounting) reads it.
func fakeCfgWithStore(st *store.Store, scripts ...fakellm.Script) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:           fakellm.New(scripts...),
			ModeLabel:     "code",
			Store:         st,
			ContextWindow: 4096,
		}
	}
}

// TestResume_CarriesPriorContext proves end-to-end (via the fakellm harness)
// that resuming a session by id (SessionOpts.ResumeID) loads the prior
// conversation and that a second turn accumulates into the SAME session's
// persisted messages — i.e. context carries over under one session id.
//
// Two runtimes are used deliberately: newTestRuntime's sessionConfig calls
// mk() per session, and each mk() builds a fresh fakellm with the full script
// list (consuming script[0] first). Splitting the first run and the resumed
// run across two runtimes — both sharing the SAME *store.Store — gives each
// turn its own script without script-sharing ambiguity.
func TestResume_CarriesPriorContext(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// First run: fresh session, one turn.
	rtA := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "noted"}}}))
	sA, err := rtA.Session(SessionOpts{Name: "a", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for range sA.Send(context.Background(), "remember the number 42") {
	}
	id := sA.sess.ID()
	if id == 0 {
		t.Fatal("first session has no store id; persistence cannot be proven")
	}

	// Sanity: the first turn persisted (>= user + assistant).
	msgs, err := st.LoadSessionMessages(id)
	if err != nil || len(msgs) < 2 {
		t.Fatalf("first run didn't persist: len=%d err=%v", len(msgs), err)
	}

	// Resume: a new session bound to the same id, second turn.
	rtB := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "it was 42"}}}))
	sB, err := rtB.Session(SessionOpts{Name: "b", WorkDir: t.TempDir(), ResumeID: id})
	if err != nil {
		t.Fatal(err)
	}
	// The resumed session must have been seeded with the prior conversation.
	if got := len(sB.sess.Messages()); got < len(msgs) {
		t.Fatalf("resume did not seed prior context: in-memory=%d, persisted before=%d", got, len(msgs))
	}
	for range sB.Send(context.Background(), "what was the number") {
	}

	// Assert carryover: the same id now holds both turns, first user msg intact.
	final, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) <= len(msgs) {
		t.Fatalf("resume did not accumulate under one session: before=%d after=%d", len(msgs), len(final))
	}
	if !strings.Contains(final[0].Content, "remember the number 42") {
		t.Fatalf("first user message lost on resume: %#v", final[0])
	}
}
