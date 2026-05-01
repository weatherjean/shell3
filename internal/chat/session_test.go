package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// statusLine format: "provider │ model"
const (
	statusSonnet = "openai │ claude-sonnet-4-6" // 1M token window
	statusOpus   = "openai │ claude-opus-4-7"   // 1M token window
	statusGPT4o  = "openai │ gpt-4o"            // 128k token window
)

func TestReminderTracker_NoEmitOnFirstTurn(t *testing.T) {
	var r reminderTracker
	got := r.check(statusSonnet, 0)
	if got != "" {
		t.Errorf("expected empty on first turn, got %q", got)
	}
}

func TestReminderTracker_ContextBucket(t *testing.T) {
	var r reminderTracker
	r.lastModel = "claude-sonnet-4-6"
	r.lastContextPct = 0
	r.lastTokens = 1000

	// 1M window: 10% = 100k tokens. Push into 10% bucket.
	got := r.check(statusSonnet, 110_000)
	if got == "" {
		t.Fatal("expected reminder at 10% bucket, got empty")
	}
	if !strings.Contains(got, "context:") {
		t.Errorf("expected context line, got %q", got)
	}
	if r.lastContextPct != 10 {
		t.Errorf("expected lastContextPct=10, got %d", r.lastContextPct)
	}
}

func TestReminderTracker_NoRepeatSameBucket(t *testing.T) {
	var r reminderTracker
	r.lastModel = "claude-sonnet-4-6"
	r.lastContextPct = 10
	r.lastTokens = 110_000

	// Still in 10% bucket, delta < 30k — no reminder.
	got := r.check(statusSonnet, 125_000) // delta = 15k, bucket still 10%
	if got != "" {
		t.Errorf("expected no reminder in same bucket, got %q", got)
	}
}

func TestReminderTracker_ModelChange(t *testing.T) {
	var r reminderTracker
	r.lastModel = "claude-opus-4-7"

	got := r.check(statusSonnet, 0)
	if got == "" {
		t.Fatal("expected reminder on model change, got empty")
	}
	if !strings.Contains(got, "model changed") {
		t.Errorf("expected model-change line, got %q", got)
	}
	if !strings.Contains(got, "claude-opus-4-7") || !strings.Contains(got, "claude-sonnet-4-6") {
		t.Errorf("expected both model names in reminder, got %q", got)
	}
}

func TestReminderTracker_30kDeltaThreshold(t *testing.T) {
	// Use gpt-4o (128k window). At 5% (6400 tokens) bucket stays 0.
	// A 30k token jump from lastTokens=100 should fire.
	var r reminderTracker
	r.lastModel = "gpt-4o"
	r.lastContextPct = 0 // never emitted a context reminder
	r.lastTokens = 100

	// 30001 tokens — delta >= 30k, but bucket check: 30001/128000 = 23% → bucket 20 > 0
	// so bucket condition fires before the delta condition anyway.
	// Use a tiny initial send to isolate delta: stay inside first bucket.
	// gpt-4o 128k: 1% = 1280 tokens. Jump 30k from 100 → 30100 = 23% → new bucket.
	// To test PURE delta: use very large window where 30k < 10%.
	// claude-sonnet-4-6 1M: 10% = 100k. 30100 tokens = 3% → bucket 0, same bucket.
	// But lastContextPct=0 means "never emitted" — bucket check: 0 > 0 is FALSE.
	// Delta check: tokenDelta >= 30000 && lastContextPct > 0 → also FALSE (lastContextPct=0).
	// So we need lastContextPct > 0 for pure delta test.
	r2 := reminderTracker{
		lastModel:      "claude-sonnet-4-6",
		lastContextPct: 10,
		lastTokens:     100_000,
	}
	got := r2.check(statusSonnet, 130_001) // delta = 30001, still bucket 10 (13%)
	if got == "" {
		t.Fatal("expected reminder on 30k token delta, got empty")
	}
	if !strings.Contains(got, "context:") {
		t.Errorf("expected context line in delta reminder, got %q", got)
	}
	_ = r
}

func TestInjectReminder_AppendsToLastUser(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
		{Role: llm.RoleUser, Content: "follow up"},
	}
	reminder := "<system-reminder>\ntest\n</system-reminder>"
	result := injectReminder(msgs, reminder)

	last := result[len(result)-1]
	if last.Role != llm.RoleUser {
		t.Fatalf("last message not user, got %s", last.Role)
	}
	if !strings.Contains(last.Content, "follow up") || !strings.Contains(last.Content, "<system-reminder>") {
		t.Errorf("unexpected last message content: %q", last.Content)
	}
	// Original earlier messages untouched.
	if result[1].Content != "hello" {
		t.Errorf("earlier user message was mutated: %q", result[1].Content)
	}
}

func TestInjectReminder_EmptyReminder(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	result := injectReminder(msgs, "")
	if result[0].Content != "hi" {
		t.Errorf("empty reminder mutated message: %q", result[0].Content)
	}
}
