//go:build unix

package telegram

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// drainTurn must return only the FINAL assistant message of a turn: narration
// emitted before tool calls ("Let me check…") is progress talk, not the reply.
func TestDrainTurnKeepsOnlyFinalSegment(t *testing.T) {
	ch := make(chan shell3.Event, 8)
	ch <- shell3.Event{Kind: shell3.Token, Text: "Let me check what completed."}
	ch <- shell3.Event{Kind: shell3.ToolCall, ToolName: "task_list"}
	ch <- shell3.Event{Kind: shell3.ToolResult, ToolName: "task_list"}
	ch <- shell3.Event{Kind: shell3.Token, Text: "No tasks running. Checking history."}
	ch <- shell3.Event{Kind: shell3.ToolCall, ToolName: "bash"}
	ch <- shell3.Event{Kind: shell3.ToolResult, ToolName: "bash"}
	ch <- shell3.Event{Kind: shell3.Token, Text: "That notice means a job finished."}
	close(ch)

	b := &Bot{}
	got := b.drainTurn(ch)
	if got != "That notice means a job finished." {
		t.Fatalf("want only the final segment, got %q", got)
	}
}

// A turn whose final segment is empty (model ends on a tool call) falls back
// to the last non-empty segment rather than replying with nothing.
func TestDrainTurnFallsBackToLastNonEmpty(t *testing.T) {
	ch := make(chan shell3.Event, 4)
	ch <- shell3.Event{Kind: shell3.Token, Text: "Done — files updated."}
	ch <- shell3.Event{Kind: shell3.ToolCall, ToolName: "bash"}
	ch <- shell3.Event{Kind: shell3.ToolResult, ToolName: "bash"}
	close(ch)

	b := &Bot{}
	if got := b.drainTurn(ch); got != "Done — files updated." {
		t.Fatalf("want fallback to last non-empty segment, got %q", got)
	}
}

// Errors surface in the reply even when they arrive before later segments.
func TestDrainTurnAppendsErrors(t *testing.T) {
	ch := make(chan shell3.Event, 4)
	ch <- shell3.Event{Kind: shell3.Token, Text: "Trying."}
	ch <- shell3.Event{Kind: shell3.Error, Err: errFake("boom")}
	close(ch)

	b := &Bot{}
	got := b.drainTurn(ch)
	if !strings.Contains(got, "Trying.") || !strings.Contains(got, "boom") {
		t.Fatalf("want text and error, got %q", got)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
