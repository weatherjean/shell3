package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

const (
	modelSonnet = "claude-sonnet-4-6"
	modelOpus   = "claude-opus-4-7"
)

const sonnetContextWindow = 1_000_000

func TestReminderTracker_NoEmitOnFirstTurn(t *testing.T) {
	var r reminderTracker
	got := r.check(modelSonnet, sonnetContextWindow, 0)
	if got != "" {
		t.Errorf("expected empty on first turn, got %q", got)
	}
}

func TestReminderTracker_ContextBucket(t *testing.T) {
	var r reminderTracker
	r.lastModel = modelSonnet
	r.lastContextPct = 0
	r.lastTokens = 1000

	got := r.check(modelSonnet, sonnetContextWindow, 110_000)
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
	r.lastModel = modelSonnet
	r.lastContextPct = 10
	r.lastTokens = 110_000

	got := r.check(modelSonnet, sonnetContextWindow, 125_000)
	if got != "" {
		t.Errorf("expected no reminder in same bucket, got %q", got)
	}
}

func TestReminderTracker_ModelChange(t *testing.T) {
	var r reminderTracker
	r.lastModel = modelOpus

	got := r.check(modelSonnet, sonnetContextWindow, 0)
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
	var r reminderTracker
	r.lastModel = "gpt-4o"
	r.lastContextPct = 0
	r.lastTokens = 100

	r2 := reminderTracker{
		lastModel:      modelSonnet,
		lastContextPct: 10,
		lastTokens:     100_000,
	}
	got := r2.check(modelSonnet, sonnetContextWindow, 130_001)
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
