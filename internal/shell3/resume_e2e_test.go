package shell3

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
)

// fakeCfgWithStore mirrors fakeCfg but wires a shared file-native runs Store so
// turns persist their message stream. ContextWindow is set because newSession's
// ContextWindowFor closure (and the turn's reminder accounting) reads it.
func fakeCfgWithStore(st *runs.Store, scripts ...fakellm.Script) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        fakellm.New(scripts...),
			ModeLabel:  "code",
			Store:      st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}
	}
}

// openTestStore opens a fresh file-native runs store rooted in a temp dir.
func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestResume_CarriesPriorContext proves end-to-end (via the fakellm harness)
// that resuming a session by id (SessionOpts.ResumeID) loads the prior
// conversation and that a second turn accumulates into the SAME session's
// persisted messages — i.e. context carries over under one session id.
//
// Two runtimes are used deliberately: newTestRuntime's sessionConfig calls
// mk() per session, and each mk() builds a fresh fakellm with the full script
// list (consuming script[0] first). Splitting the first run and the resumed
// run across two runtimes — both sharing the SAME *runs.Store — gives each
// turn its own script without script-sharing ambiguity.
func TestResume_CarriesPriorContext(t *testing.T) {
	st := openTestStore(t)

	// First run: fresh session, one turn.
	rtA := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "noted"}}}))
	sA, err := rtA.Session(SessionOpts{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for range sA.Send(context.Background(), "remember the number 42") {
	}
	id := sA.sess.ID()
	if id == "" {
		t.Fatal("first session has no store id; persistence cannot be proven")
	}

	// Sanity: the first turn persisted (>= user + assistant).
	msgs, err := st.LoadMessages(id)
	if err != nil || len(msgs) < 2 {
		t.Fatalf("first run didn't persist: len=%d err=%v", len(msgs), err)
	}

	// Resume: a new session bound to the same id, second turn.
	rtB := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "it was 42"}}}))
	sB, err := rtB.Session(SessionOpts{WorkDir: t.TempDir(), ResumeID: id})
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
	final, err := st.LoadMessages(id)
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

// TestResumeLatest_ReattachesNewest proves SessionOpts.ResumeLatest rejoins the
// most recent session sharing the same workdir (the front-end restart path) and
// reports Resumed(), instead of spawning a fresh empty row.
func TestResumeLatest_ReattachesNewest(t *testing.T) {
	st := openTestStore(t)

	wd := t.TempDir() // the shared "front-end" workdir both boots use

	// First boot: fresh session, one turn that persists.
	rtA := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "noted"}}}))
	sA, err := rtA.Session(SessionOpts{WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	for range sA.Send(context.Background(), "remember 42") {
	}
	id := sA.sess.ID()
	if id == "" {
		t.Fatal("first session has no store id")
	}

	// Second boot: ResumeLatest must reattach to id, not create a new row.
	rtB := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "still 42"}}}))
	sB, err := rtB.Session(SessionOpts{WorkDir: wd, ResumeLatest: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := sB.sess.ID(); got != id {
		t.Fatalf("ResumeLatest: attached to session %s, want reattach to %s", got, id)
	}
	if sB.MessageCount() == 0 {
		t.Fatal("reattached session reloaded no messages")
	}
}

// TestResumeLatest_SkipsSubagentChild proves that a subagent child session —
// which shares the parent's workdir and is created (newer) mid-run — is never
// the target of a resume-latest restart. Without the ParentID guard the newer
// child would win and the front-end would reattach to the subagent transcript,
// silently replacing the user's conversation.
func TestResumeLatest_SkipsSubagentChild(t *testing.T) {
	st := openTestStore(t)
	wd := t.TempDir()

	// Main front-end session with one persisted turn.
	rtA := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "noted"}}}))
	sMain, err := rtA.Session(SessionOpts{WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	for range sMain.Send(context.Background(), "remember 42") {
	}
	mainID := sMain.sess.ID()

	// A subagent child: same workdir, ParentID set, created after (newer id).
	sChild, err := rtA.Session(SessionOpts{WorkDir: wd, Headless: true, ParentID: mainID})
	if err != nil {
		t.Fatal(err)
	}
	if sChild.sess.ID() == mainID || sChild.sess.ID() == "" {
		t.Fatalf("child session id %q must be distinct from main %q", sChild.sess.ID(), mainID)
	}

	// Restart with ResumeLatest: must rejoin the main session, skipping the
	// newer child.
	rtB := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "still 42"}}}))
	sB, err := rtB.Session(SessionOpts{WorkDir: wd, ResumeLatest: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := sB.sess.ID(); got != mainID {
		t.Fatalf("ResumeLatest attached to %s, want the main session %s (not the subagent child)", got, mainID)
	}
}

// TestResumeLatest_NoMatchStartsFresh verifies ResumeLatest falls back to a new
// session when nothing matches the workdir.
func TestResumeLatest_NoMatchStartsFresh(t *testing.T) {
	st := openTestStore(t)

	rt := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "hi"}}}))
	s, err := rt.Session(SessionOpts{WorkDir: t.TempDir(), ResumeLatest: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.sess.ID() == "" {
		t.Fatal("expected a fresh non-empty session id")
	}
	if s.MessageCount() != 0 {
		t.Fatal("no prior session existed, but history is non-empty")
	}
}
