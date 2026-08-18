package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestRunTurn_AutoCompact_CronJobSurvivesRoll drives a REAL compaction through
// RunTurn (not a hand-built post-roll row): a cron-dispatched session runs one
// ordinary turn, then a second turn whose lastPromptTokens trips auto-compaction
// and rolls onto a new runs-store session. Without carrying CronJob (and Agent/
// ParentID) into compactInto's NewSession call, the rolled row comes back with
// cron_job="" and CronRollup silently drops the majority of a tool-heavy job's
// spend the moment it compacts — exactly the job this branch exists to make
// visible. This test would fail RED (rolled row cron_job="") before the fix in
// chat.go/toolhandler.go/compact.go/shell3/session.go carrying Agent/ParentID/
// CronJob through the roll.
func TestRunTurn_AutoCompact_CronJobSurvivesRoll(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	origID, err := st.NewSession(runs.Meta{CronJob: "nightly-sync", Agent: "syncer", Model: "test-model"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	fake := fakellm.New(
		// Turn 1: ordinary turn, no compaction (lastPromptTokens starts 0).
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-1"},
			{Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
		}},
		// Turn 2, call 0: the quiet compaction summary of the head.
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
		// Turn 2, call 1: the user's turn, answered against the compacted history.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-2"},
			{Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220}},
		}},
	)

	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		ConfigDir:   "/cfg",
		Agent:       "syncer",
		CronJob:     "nightly-sync",
		AgentKnobs:  AgentKnobs{CompactAt: 100, KeepRecent: 25},
		ToolConfig:  ToolConfig{Store: st},
	}

	sess := NewSession(SessionOpts{StoreID: origID, Store: st})
	// Mirrors Session.RunParts's persist closure: RunTurn itself does not
	// flush to the store, the caller's beforeDone does (see turn.go's doc
	// comment on beforeDone).
	persist := func() { saveHistory(cfg.Store, LogOrNoop(nil), sess, sess.ID()) }

	// Turn 1: adds usage to the ORIGINAL session row (below CompactAt, no roll).
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q1"}, persist)
	if sess.ID() != origID {
		t.Fatalf("turn 1 must not have rolled the session, got id=%s", sess.ID())
	}

	// Pad history so the compaction cut clears compactionFloor, then force
	// compaction on turn 2 by raising lastPromptTokens past CompactAt.
	big := strings.Repeat("y", 40)
	var filler []llm.Message
	for range 12 {
		filler = append(filler, llm.Message{Role: llm.RoleAssistant, Content: big})
	}
	sess.messages = append(filler, sess.messages...)
	sess.lastPromptTokens = 500

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q2"}, persist)

	rolledID := sess.ID()
	if rolledID == origID {
		t.Fatalf("turn 2 should have compacted and rolled the session, still on %s", origID)
	}

	metas, err := st.ListSessions(10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var rolledMeta *runs.Meta
	for i := range metas {
		if metas[i].ID == rolledID {
			rolledMeta = &metas[i]
		}
	}
	if rolledMeta == nil {
		t.Fatalf("rolled session %s not found in store", rolledID)
	}
	if rolledMeta.CronJob != "nightly-sync" {
		t.Fatalf("rolled session cron_job = %q, want %q — attribution lost at the compaction boundary", rolledMeta.CronJob, "nightly-sync")
	}
	if rolledMeta.Agent != "syncer" {
		t.Fatalf("rolled session agent = %q, want %q", rolledMeta.Agent, "syncer")
	}

	// CronRollup must see the WHOLE spend (both the pre-compaction fragment on
	// origID and the post-compaction fragment on rolledID), not just the
	// smaller slice left on one row.
	costs, err := st.CronRollup(time.Time{})
	if err != nil {
		t.Fatalf("cron rollup: %v", err)
	}
	if len(costs) != 1 {
		t.Fatalf("want exactly 1 job in rollup, got %d: %+v", len(costs), costs)
	}
	got := costs[0]
	if got.CronJob != "nightly-sync" {
		t.Fatalf("rollup cron_job = %q, want %q", got.CronJob, "nightly-sync")
	}
	wantPrompt := int64(100 + 200)
	wantCompletion := int64(10 + 20)
	if got.PromptTokens != wantPrompt || got.CompletionTokens != wantCompletion {
		t.Fatalf("rollup tokens = %d prompt / %d completion, want %d / %d (must sum both sides of the compaction boundary, not just the post-roll fragment)",
			got.PromptTokens, got.CompletionTokens, wantPrompt, wantCompletion)
	}
	if got.Runs != 2 {
		t.Fatalf("rollup runs = %d, want 2 (pre- and post-compaction sessions both counted)", got.Runs)
	}
}
