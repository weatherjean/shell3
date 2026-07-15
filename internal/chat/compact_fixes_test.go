package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestCompactInto_ResetsReminderLog: compaction rewrites history to a short
// continuation+tail, so the reminderLog anchors (which index the pre-compaction
// message slice) are stale. They must be dropped — exactly as SetMessages does —
// or History()'s non-decreasing-Seq interleave silently hides every later
// reminder from the dashboard.
func TestCompactInto_ResetsReminderLog(t *testing.T) {
	sess := NewSession(SessionOpts{})
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
		{Role: llm.RoleAssistant, Content: "d"},
	}
	sess.reminderLog = []ReminderRecord{{Seq: 1, Text: "old reminder"}, {Seq: 3, Text: "old reminder 2"}}
	tail := sess.messages[2:]

	compactInto(CompactSummary{Summary: "did stuff"}, nil, sess, tail, applog.Noop{}, "", "", "")

	if len(sess.reminderLog) != 0 {
		t.Fatalf("reminderLog must be reset after compaction (stale anchors break History), got %+v", sess.reminderLog)
	}
}

// TestCompactInto_AbortsOnFailedRoll: when the runs store cannot start the new
// session (e.g. a full disk), compaction must abort cleanly — leaving the
// in-memory history and the outgoing session's JSONL untouched — rather than
// rewrite memory to a short slice while the old file still holds the full
// history (which the next saveHistory would then duplicate into).
func TestCompactInto_AbortsOnFailedRoll(t *testing.T) {
	dir := t.TempDir()
	st, err := runs.Open(dir)
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sess := NewSession(SessionOpts{StoreID: id})
	orig := []llm.Message{
		{Role: llm.RoleUser, Content: "keep 1"},
		{Role: llm.RoleAssistant, Content: "keep 2"},
		{Role: llm.RoleUser, Content: "keep 3"},
	}
	sess.messages = orig

	// Make NewSession fail: the runs/ dir is read-only, so MkdirAll for a fresh
	// session subdir is denied. (The existing session subdir keeps its own perms.)
	runsDir := filepath.Join(dir, "runs")
	if err := os.Chmod(runsDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(runsDir, 0o755)

	ok := compactInto(CompactSummary{Summary: "should not apply"}, st, sess, sess.messages[1:], applog.Noop{}, "", "", "")
	if ok {
		t.Fatal("compactInto should report failure when the session roll fails")
	}
	if len(sess.messages) != len(orig) || sess.messages[0].Content != "keep 1" {
		t.Fatalf("history must be untouched on a failed roll, got %+v", sess.messages)
	}
	if sess.id != id {
		t.Fatalf("session id must be unchanged on a failed roll, got %q want %q", sess.id, id)
	}
}

// TestRunTurn_QueuedCompact_FewLargeMessages: a forced :compact on a history of
// few but very large messages (head < compactionFloor messages, yet huge in
// tokens) must still compact. The message-count floor alone would silently
// no-op, leaving the user unable to reclaim context exactly when they need to.
func TestRunTurn_QueuedCompact_FewLargeMessages(t *testing.T) {
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
		// auto-compaction would not trigger; tiny tail.
		AgentKnobs: AgentKnobs{CompactAt: 100000, KeepRecent: 20},
	}
	sess, c := newCollectorSession(SessionOpts{})
	big := strings.Repeat("x", 8000) // ~2000 tokens each
	for i := 0; i < 5; i++ {         // only 5 head messages → cut < compactionFloor (8)
		sess.messages = append(sess.messages, llm.Message{Role: llm.RoleAssistant, Content: big})
	}
	sess.messages = append(sess.messages, llm.Message{Role: llm.RoleAssistant, Content: "LATEST_TAIL_MARKER"})
	sess.lastPromptTokens = 50000
	sess.QueueCompact()

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("forced :compact must compact a few-but-huge-message history")
	}
	if !msgsContain(sess.messages, "SUMMARY of the head") {
		t.Fatalf("forced compact should inject the head summary: %+v", sess.messages)
	}
	if !msgsContain(sess.messages, "LATEST_TAIL_MARKER") {
		t.Fatalf("the latest turn must be preserved as tail: %+v", sess.messages)
	}
}

// TestCompactNow_ResetsContextGauge: after compaction the prompt-token gauge
// drops to a small estimate; the reminderTracker's context-usage state (the last
// emitted bucket / token mark) must reset too, or context-usage reminders are
// suppressed across the whole band the conversation re-grows through.
func TestCompactNow_ResetsContextGauge(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY"}}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		AgentKnobs:  AgentKnobs{CompactAt: 100, KeepRecent: 25},
	}
	sess, _ := newCollectorSession(SessionOpts{})
	seedHistory(sess, "MARKER", 500)
	// Simulate a tracker that already emitted at a high bucket before compaction.
	sess.reminders.lastContextPct = 80
	sess.reminders.lastTokens = 90000

	compactNow(context.Background(), cfg, sess, true)

	if sess.reminders.lastContextPct != 0 || sess.reminders.lastTokens != 0 {
		t.Fatalf("context gauge must reset after compaction, got pct=%d tokens=%d",
			sess.reminders.lastContextPct, sess.reminders.lastTokens)
	}
}

// TestAddUsage_KeepsPromptWhenRoundOmitsIt: some providers stream a follow-up
// round whose final usage carries completion tokens but PromptTokens=0. addUsage
// must not zero the accumulated prompt count (and the derived total) in that case.
func TestAddUsage_KeepsPromptWhenRoundOmitsIt(t *testing.T) {
	got := addUsage(
		llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
		llm.Usage{PromptTokens: 0, CompletionTokens: 5, TotalTokens: 5},
	)
	if got.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100 (kept from prior round)", got.PromptTokens)
	}
	if got.CompletionTokens != 15 {
		t.Errorf("CompletionTokens = %d, want 15", got.CompletionTokens)
	}
	if got.TotalTokens != 115 {
		t.Errorf("TotalTokens = %d, want 115", got.TotalTokens)
	}
}
