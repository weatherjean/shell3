package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// standaloneCfg is the shared TurnConfig for CompactStandalone tests: small
// KeepRecent so seeded history has a head to summarize.
func standaloneCfg(fake *fakellm.Client) TurnConfig {
	return TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		AgentKnobs:  AgentKnobs{CompactAt: 100000, KeepRecent: 20},
	}
}

// TestCompactStandalone_ReportsDelta pins the manual /compact path: with a
// compactable history it summarises the head, reports a positive token delta,
// and injects the continuation summary.
func TestCompactStandalone_ReportsDelta(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
	)
	sess, _ := newCollectorSession(SessionOpts{})
	// A big head so before > after even with the summary added.
	big := strings.Repeat("x", 2000)
	for i := 0; i < 20; i++ {
		sess.messages = append(sess.messages,
			llm.Message{Role: llm.RoleUser, Content: big},
			llm.Message{Role: llm.RoleAssistant, Content: big},
		)
	}
	sess.messages = append(sess.messages, llm.Message{Role: llm.RoleAssistant, Content: "TAIL_MARKER"})

	before, after, err := CompactStandalone(context.Background(), standaloneCfg(fake), sess)
	if err != nil {
		t.Fatalf("CompactStandalone: %v", err)
	}
	if after <= 0 || before <= after {
		t.Fatalf("want before > after > 0, got before=%d after=%d", before, after)
	}
	if !msgsContain(sess.messages, "<compact-summary>") {
		t.Fatalf("compacted history should carry the summary block: %+v", sess.messages[0].Content)
	}
	if !msgsContain(sess.messages, "TAIL_MARKER") {
		t.Fatal("the verbatim tail should survive a standalone compact")
	}
}

// TestCompactStandalone_NothingToCompact pins the empty/near-empty case: no
// head to summarise returns ErrNothingToCompact and leaves history untouched.
func TestCompactStandalone_NothingToCompact(t *testing.T) {
	fake := fakellm.New() // no scripts: any LLM call would fail the test
	sess, _ := newCollectorSession(SessionOpts{})
	sess.messages = []llm.Message{{Role: llm.RoleUser, Content: "hi"}}

	_, _, err := CompactStandalone(context.Background(), standaloneCfg(fake), sess)
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("want ErrNothingToCompact, got %v", err)
	}
	if len(sess.messages) != 1 || sess.messages[0].Content != "hi" {
		t.Fatalf("history must be untouched, got %+v", sess.messages)
	}
}

// TestCompactStandalone_LLMFailureLeavesHistory pins the failure contract: a
// summarisation error reports the error and leaves history untouched.
func TestCompactStandalone_LLMFailureLeavesHistory(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Err: errors.New("provider down")})
	sess, _ := newCollectorSession(SessionOpts{})
	big := strings.Repeat("x", 2000)
	for i := 0; i < 20; i++ {
		sess.messages = append(sess.messages,
			llm.Message{Role: llm.RoleUser, Content: big},
			llm.Message{Role: llm.RoleAssistant, Content: big},
		)
	}
	wantLen := len(sess.messages)
	first := sess.messages[0].Content

	_, _, err := CompactStandalone(context.Background(), standaloneCfg(fake), sess)
	if err == nil || errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("want a hard error, got %v", err)
	}
	if len(sess.messages) != wantLen || sess.messages[0].Content != first {
		t.Fatal("history must be untouched after an LLM failure")
	}
}
