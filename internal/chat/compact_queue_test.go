package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestRunTurn_QueuedCompact_ForcesBelowThreshold pins that QueueCompact (the TUI
// :compact command) compacts at the next turn even though the prompt-token count
// is far below compact_at — the user explicitly asked for it.
func TestRunTurn_QueuedCompact_ForcesBelowThreshold(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		// high CompactAt: auto-compaction would NOT trigger on its own; small
		// KeepRecent tail so the seeded history has a head to summarize.
		AgentKnobs: AgentKnobs{CompactAt: 100000, KeepRecent: 20},
	}
	sess, c := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 500) // 500 << 100000
	sess.QueueCompact()
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("queued :compact should force a compaction even below the threshold")
	}
	// One-shot: maybeCompact consumes the request via forceCompact.Swap(false),
	// so a later turn without a new queue won't compact again.
	if sess.forceCompact.Load() {
		t.Fatal("queued compaction flag should be cleared after it fires")
	}
}

// TestRunTurn_QueuedCompact_ZeroCompactAtKeepsTail pins the minKeepRecent floor:
// a forced :compact while auto-compaction is OFF (compact_at=0, so keep_recent
// resolves to 0) must still preserve a verbatim tail rather than summarizing the
// most recent turns away.
func TestRunTurn_QueuedCompact_ZeroCompactAtKeepsTail(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of the head"}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		AgentKnobs:  AgentKnobs{CompactAt: 0}, // auto-compaction off; only the forced path can compact
	}
	sess, c := newCollectorSession(SessionOpts{})
	// History long enough that the floor yields a real head/tail split: big
	// messages so the preserved tail crosses minKeepRecent tokens.
	big := strings.Repeat("x", 1200)
	for i := 0; i < 30; i++ {
		sess.messages = append(sess.messages,
			llm.Message{Role: llm.RoleUser, Content: big},
			llm.Message{Role: llm.RoleAssistant, Content: big},
		)
	}
	sess.messages = append(sess.messages, llm.Message{Role: llm.RoleAssistant, Content: "LATEST_TAIL_MARKER"})
	sess.lastPromptTokens = 99999
	sess.QueueCompact()
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("forced compact should fire even with compact_at=0")
	}
	// The compacted event must carry the post-compaction estimate (so a UI can
	// drop its meter at once); it should be a positive count well below the
	// pre-compaction lastPromptTokens.
	var est int
	for _, e := range c.all() {
		if e.Kind == EventCompacted && e.Usage != nil {
			est = e.Usage.PromptTokens
		}
	}
	if est <= 0 || est >= 99999 {
		t.Fatalf("compacted event should carry a positive, reduced token estimate, got %d", est)
	}
	if !msgsContain(sess.messages, "LATEST_TAIL_MARKER") {
		t.Fatalf("floor failed: the latest turn was summarized away: %+v", sess.messages)
	}
	if !msgsContain(sess.messages, "SUMMARY of the head") {
		t.Fatalf("forced compact should inject the head summary: %+v", sess.messages)
	}
}
