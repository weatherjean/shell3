//go:build unix

package telegram

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
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
	got, _ := tconv(b).drainTurn(ch, true)
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
	if got, _ := tconv(b).drainTurn(ch, true); got != "Done — files updated." {
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
	got, errText := tconv(b).drainTurn(ch, true)
	if !strings.Contains(got, "Trying.") || !strings.Contains(errText, "boom") {
		t.Fatalf("want text and error, got %q / %q", got, errText)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// chunk budgets in UTF-16 code units, Telegram's actual accounting: emoji
// (astral plane) count double, so a 3000-emoji string is 6000 units and must
// split even though it is only 3000 runes.
func TestChunkCountsUTF16(t *testing.T) {
	emoji := strings.Repeat("🚀", 3000)
	parts := chunk(emoji)
	if len(parts) < 2 {
		t.Fatalf("6000 UTF-16 units must split, got %d chunk(s)", len(parts))
	}
	for i, p := range parts {
		if utf16Len(p) > tgMaxMessage {
			t.Fatalf("chunk %d is %d UTF-16 units (cap %d)", i, utf16Len(p), tgMaxMessage)
		}
		for _, r := range p {
			if r == 0xFFFD {
				t.Fatalf("chunk %d contains a broken rune", i)
			}
		}
	}
	// ASCII within budget stays whole.
	if got := chunk(strings.Repeat("a", tgMaxMessage)); len(got) != 1 {
		t.Fatalf("ASCII at exactly the cap must stay one chunk, got %d", len(got))
	}
}

// The silence sentinel survives reasoning-split mangling: a provider that
// swallows the reply's first tokens leaves a tail fragment ("_REPLY"), which
// must still read as silence. Real text never does.
func TestIsNoReplyMangledTails(t *testing.T) {
	for _, s := range []string{"NO_REPLY", "no_reply.", "NO_REPLY!", "`NO_REPLY`", "_REPLY", "O_REPLY", "REPLY", ""} {
		if !strutil.IsNoReply(s) {
			t.Errorf("IsNoReply(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"PLY", "the reply is ready", "NO_REPLY needed here, sending anyway", "All done — see summary"} {
		if strutil.IsNoReply(s) {
			t.Errorf("IsNoReply(%q) = true, want false", s)
		}
	}
}
