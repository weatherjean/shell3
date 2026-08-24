package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// A forced compaction (CompactStandalone — the front-end's /compact) summarises
// even though the prompt-token count is far below compact_at: the user asked
// for it explicitly.
func TestCompactStandalone_ForcesBelowThreshold(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		// high CompactAt: auto-compaction would NOT trigger on its own; small
		// KeepRecent tail so the seeded history has a head to summarize.
		AgentKnobs: AgentKnobs{CompactAt: 100000, KeepRecent: 20},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
	}
	sess, c := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 500) // 500 << 100000

	if _, _, err := CompactStandalone(context.Background(), cfg, sess); err != nil {
		t.Fatalf("CompactStandalone: %v", err)
	}
	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("a forced compaction should emit EventCompacted even below the threshold")
	}
	// The head really was replaced by the summary, not merely re-emitted.
	if msgsContain(sess.messages, "PRE_COMPACT_MARKER") {
		t.Fatalf("the head should have been summarized away: %+v", sess.messages)
	}
	if !msgsContain(sess.messages, "SUMMARY of prior work") {
		t.Fatalf("the summary should be injected: %+v", sess.messages)
	}
}

// TestCompactStandalone_ZeroCompactAtKeepsTail pins the minKeepRecent floor: a
// forced /compact while auto-compaction is OFF (compact_at=0, so keep_recent
// resolves to 0) must still preserve a verbatim tail rather than summarizing
// the most recent turns away.
func TestCompactStandalone_ZeroCompactAtKeepsTail(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of the head"}}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 0}, // auto-compaction off; only the forced path can compact
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
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

	if _, _, err := CompactStandalone(context.Background(), cfg, sess); err != nil {
		t.Fatalf("CompactStandalone: %v", err)
	}
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
