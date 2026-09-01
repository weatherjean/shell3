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

// fakeCfgWithStore mirrors fakeCfg but wires a shared SQLite runs Store so
// turns persist their message stream. ContextWindow feeds the turn's reminder
// accounting directly.
func fakeCfgWithStore(st *runs.Store, scripts ...fakellm.Script) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        fakellm.New(scripts...),
			Agent:      "code",
			ModelID:    "test-model",
			Store:      st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}
	}
}

func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestResume_CarriesPriorContext(t *testing.T) {
	st := openTestStore(t)

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
	meta, err := st.SessionMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Agent != "code" {
		t.Fatalf("stored agent = %q, want resolved config agent", meta.Agent)
	}
	if meta.Model != "test-model" {
		t.Fatalf("stored model = %q, want resolved model id", meta.Model)
	}

	msgs, err := st.LoadMessages(id)
	if err != nil || len(msgs) < 2 {
		t.Fatalf("first run didn't persist: len=%d err=%v", len(msgs), err)
	}

	rtB := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "it was 42"}}}))
	sB, err := rtB.Session(SessionOpts{WorkDir: t.TempDir(), ResumeID: id})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sB.sess.Messages()); got < len(msgs) {
		t.Fatalf("resume did not seed prior context: in-memory=%d, persisted before=%d", got, len(msgs))
	}
	for range sB.Send(context.Background(), "what was the number") {
	}

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
