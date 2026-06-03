package patchapp

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestRenderUserMessage_WrapsBeforeStylingAndFillsWidth(t *testing.T) {
	got := renderUserMessage(strings.Repeat("a", 12), 10)
	if len(got) != 2 {
		t.Fatalf("lines = %d; want 2 (%q)", len(got), got)
	}
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 10 {
			t.Fatalf("line %d visible width = %d; want 10; line=%q", i, w, l)
		}
	}
}

func TestRenderUserMessage_WideCharsFillWidth(t *testing.T) {
	got := renderUserMessage("你好世界こんにちは", 14)
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 14 {
			t.Fatalf("line %d visible width = %d; want 14; line=%q", i, w, l)
		}
	}
}

func TestRenderUserMessage_MixedWidthAlwaysFillsLine(t *testing.T) {
	msg := "mixed width: abcdefghij 👩💻🚀✨ こんにちは世界 مرحبا بالعالم"
	got := renderUserMessage(msg, 24)
	if len(got) < 2 {
		t.Fatalf("expected wrapping, got %d line(s)", len(got))
	}
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 24 {
			t.Fatalf("line %d visible width = %d; want 24; line=%q", i, w, l)
		}
	}
}

// TestHistoryRecall characterizes the up-arrow history-recall state machine
// (historyStepBackLocked) and draft mirroring (syncDraftLocked) — the intricate
// editor cluster with no other coverage. It drives the real helpers directly
// under a.mu, the same white-box style the other patchapp tests use. Pins
// current behavior ahead of moving these fields into editorState.
func TestHistoryRecall(t *testing.T) {
	stepBack := func(a *App) {
		a.mu.Lock()
		a.historyStepBackLocked()
		a.mu.Unlock()
	}

	// Walk newest -> oldest from an empty live line, then clamp at the oldest.
	t.Run("walk and clamp", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.ed.history = []string{"first", "second", "third"}

		stepBack(a)
		if got := string(a.ed.input); got != "third" || a.ed.historyIdx != 1 {
			t.Fatalf("step 1: input=%q idx=%d; want \"third\" idx=1", got, a.ed.historyIdx)
		}
		stepBack(a)
		if got := string(a.ed.input); got != "second" || a.ed.historyIdx != 2 {
			t.Fatalf("step 2: input=%q idx=%d; want \"second\" idx=2", got, a.ed.historyIdx)
		}
		stepBack(a)
		if got := string(a.ed.input); got != "first" || a.ed.historyIdx != 3 {
			t.Fatalf("step 3: input=%q idx=%d; want \"first\" idx=3", got, a.ed.historyIdx)
		}
		stepBack(a) // clamp: no entry older than the oldest
		if got := string(a.ed.input); got != "first" || a.ed.historyIdx != 3 {
			t.Fatalf("clamp: input=%q idx=%d; want \"first\" idx=3 (unchanged)", got, a.ed.historyIdx)
		}
	})

	// Draft-recovery path: input cleared but a non-empty draft remains (the
	// post-Escape state). First step-back restores the draft before history.
	t.Run("draft recovery", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.ed.history = []string{"h1"}
		a.ed.historyDraft = []rune("recovered")
		a.ed.input = a.ed.input[:0] // cleared; draft intact, input != draft

		stepBack(a)
		if got := string(a.ed.input); got != "recovered" || !a.ed.historyInDraft || a.ed.historyIdx != 0 {
			t.Fatalf("recover: input=%q inDraft=%v idx=%d; want \"recovered\" inDraft=true idx=0",
				got, a.ed.historyInDraft, a.ed.historyIdx)
		}
		stepBack(a)
		if got := string(a.ed.input); got != "h1" || a.ed.historyInDraft || a.ed.historyIdx != 1 {
			t.Fatalf("into history: input=%q inDraft=%v idx=%d; want \"h1\" inDraft=false idx=1",
				got, a.ed.historyInDraft, a.ed.historyIdx)
		}
	})

	// syncDraftLocked mirrors live input into the draft, but not while
	// navigating history (historyIdx > 0).
	t.Run("sync draft only in live mode", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.ed.input = []rune("abc")
		a.mu.Lock()
		a.syncDraftLocked()
		a.mu.Unlock()
		if got := string(a.ed.historyDraft); got != "abc" {
			t.Fatalf("live sync: draft=%q; want \"abc\"", got)
		}

		a.ed.historyIdx = 1 // now navigating history
		a.ed.input = []rune("changed")
		a.mu.Lock()
		a.syncDraftLocked()
		a.mu.Unlock()
		if got := string(a.ed.historyDraft); got != "abc" {
			t.Fatalf("nav sync: draft=%q; want \"abc\" (unchanged while navigating)", got)
		}
	})
}
