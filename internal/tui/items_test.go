package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestReducerStreamsAssistant(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "Hel"})
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "lo"})
	if len(tr.items) != 1 || tr.items[0].Text != "Hello" {
		t.Fatalf("assistant stream = %+v", tr.items)
	}
}

func TestReducerToolMergeByID(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: `{"command":"ls"}`})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolCallID: "1", ToolOutput: "out"})
	if len(tr.items) != 1 || !tr.items[0].ToolDone || tr.items[0].ToolOutput != "out" {
		t.Fatalf("tool merge = %+v", tr.items[0])
	}
}

// A tool result flagged as an error (e.g. an on_tool_call denial) must render a
// red ✗, never the green ✓ used for success.
func TestToolResultErrorRendersCross(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "rm -rf /"})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolCallID: "1",
		ToolOutput: "error: blocked by on_tool_call", ToolError: true})
	if !tr.items[0].ToolError {
		t.Fatal("ToolError should flow from the event onto the item")
	}
	out, _, _, _ := tr.renderBlocks(-1, false, 80, -1, -1)
	plain := stripANSI(out)
	if !strings.Contains(plain, "✗") {
		t.Fatalf("denied/errored tool result should render ✗:\n%s", plain)
	}
	if strings.Contains(plain, "✓") {
		t.Fatalf("denied/errored tool result must not render ✓:\n%s", plain)
	}
}

func TestReducerInterleaving(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "before"})
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1"})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "ok"})
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "after"})
	if len(tr.items) != 3 || tr.items[0].Text != "before" || tr.items[2].Text != "after" {
		t.Fatalf("interleave = %d items %+v", len(tr.items), tr.items)
	}
}

func TestRenderIncludesContent(t *testing.T) {
	tr := NewTranscript()
	tr.AddUser("hi there")
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls -la"})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "file.go"})
	// Unfold so input/output show; folded would only show the header.
	tr.FoldAll(false)
	out, starts, _, _ := tr.renderBlocks(0, false, 80, -1, -1)
	if len(starts) != tr.count() {
		t.Fatalf("starts should have one entry per block: %d vs %d", len(starts), tr.count())
	}
	plain := stripANSI(out)
	for _, want := range []string{"› hi there", "● bash", "ls -la", "file.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render missing %q in:\n%s", want, plain)
		}
	}
}

// stripANSI removes SGR escapes so tests assert on visible text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestThinkingStreamsLiveThenCollapses(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Reasoning, Text: "weighing options"})
	if tr.items[0].Folded {
		t.Fatal("thinking should be unfolded (live) while streaming")
	}
	// A token (assistant starts) ends the thinking block → it collapses.
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "answer"})
	if !tr.items[0].Folded {
		t.Fatal("thinking should collapse once the block is done")
	}
}

func TestEditFileExpandsByDefaultWithoutJSON(t *testing.T) {
	tr := NewTranscript()
	in := `{"file_path":"/tmp/x","old_string":"a","new_string":"a\nb"}`
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "edit_file", ToolCallID: "1", ToolInput: in})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolName: "edit_file",
		ToolOutput: "Edited /tmp/x (+1 -0)\n@@ -1 +1,2 @@\n a\n+b"})
	if tr.items[0].Folded {
		t.Fatal("edit_file should be expanded by default")
	}
	plain := stripANSI(mustRender(tr))
	if strings.Contains(plain, "old_string") || strings.Contains(plain, "file_path") {
		t.Fatalf("edit_file must not show its raw JSON args:\n%s", plain)
	}
	for _, want := range []string{"Edited /tmp/x", "@@ -1 +1,2 @@", "+b"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("edit_file should show header + diff, missing %q:\n%s", want, plain)
		}
	}
}

func TestToolHeaderColors(t *testing.T) {
	const sample = "● x"
	pairs := []struct {
		tool string
		want string
	}{
		{"bash", stToolBash.Render(sample)},
		{"edit_file", stToolEdit.Render(sample)},
		{"bash_bg", stToolBg.Render(sample)},
		{"websearch", stToolOther.Render(sample)}, // any other tool → pink
		{"random_tool", stToolOther.Render(sample)},
	}
	for _, p := range pairs {
		if got := toolStyle(p.tool).Render(sample); got != p.want {
			t.Fatalf("tool %q: wrong header style\n got %q\nwant %q", p.tool, got, p.want)
		}
	}
	// The four categories must be visually distinct.
	seen := map[string]bool{}
	for _, s := range []string{stToolBash.Render(sample), stToolEdit.Render(sample), stToolBg.Render(sample), stToolOther.Render(sample)} {
		if seen[s] {
			t.Fatal("tool categories should have distinct colors")
		}
		seen[s] = true
	}
}

func TestAssistantRendersMarkdown(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "- alpha\n- beta"})
	tr.Apply(shell3.Event{Kind: shell3.Done})
	plain := stripANSI(mustRender(tr))
	if !strings.Contains(plain, "•") {
		t.Fatalf("markdown list should render a bullet:\n%s", plain)
	}
}

func TestAssistantMarkdownCachedAcrossRenders(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "hello **world**"})
	tr.Apply(shell3.Event{Kind: shell3.Done})
	tr.renderBlocks(-1, false, 80, -1, -1) // populate the cache
	a := tr.items[0]
	if a.Kind != ItemAssistant || a.mdOut == "" {
		t.Fatalf("assistant block should have a cached render: %+v", a)
	}
	// Poison the cache; an unchanged (text,width) render — e.g. a scroll — must
	// reuse it and NOT re-run glamour. This is the scroll-lag fix.
	a.mdOut = "SENTINEL"
	tr.renderBlocks(-1, false, 80, -1, -1)
	if a.mdOut != "SENTINEL" {
		t.Fatal("scroll (same text+width) must reuse the cache, not re-render markdown")
	}
	// A width change must invalidate the cache.
	tr.renderBlocks(-1, false, 100, -1, -1)
	if a.mdOut == "SENTINEL" {
		t.Fatal("width change should invalidate the markdown cache")
	}
}

// TestAssistantMarkdownRecolorsOnPaletteSwitch is the regression guard for the
// adaptive-theme bug: a light/dark switch must recolor markdown that was ALREADY
// rendered under the old palette, not just newly appended blocks. The per-item
// cache is keyed on width+len (both unchanged here), so mdEpoch — bumped by
// applyPalette via resetMarkdown — is what forces the re-render.
func TestAssistantMarkdownRecolorsOnPaletteSwitch(t *testing.T) {
	t.Cleanup(func() { applyPalette(darkPalette) })
	applyPalette(darkPalette)
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "# heading"})
	tr.Apply(shell3.Event{Kind: shell3.Done})
	tr.renderBlocks(-1, false, 80, -1, -1) // render under the dark palette
	a := tr.items[0]
	darkRender := a.mdOut
	if darkRender == "" {
		t.Fatal("assistant block should have rendered")
	}
	// Same text, same width — only the palette changed. The block must re-render
	// so its colors track the new palette.
	applyPalette(lightPalette)
	tr.renderBlocks(-1, false, 80, -1, -1)
	if a.mdOut == darkRender {
		t.Fatal("a palette switch must recolor already-rendered assistant markdown")
	}
}

func mustRender(tr *Transcript) string {
	out, _, _, _ := tr.renderBlocks(-1, false, 80, -1, -1)
	return out
}

func TestTruncateSummaryRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 100) // 2-byte runes; a byte slice at 60 would split one
	out := truncateSummary(s)
	if !utf8.ValidString(out) {
		t.Fatalf("truncated summary must be valid UTF-8: %q", out)
	}
	if got := utf8.RuneCountInString(strings.TrimSuffix(out, "…")); got != 60 {
		t.Fatalf("should keep 60 runes, got %d", got)
	}
}

// TestTruncateSummaryBoundary pins the exact 60-rune budget: 60 passes
// through, 61 and beyond become first-60 + ellipsis.
func TestTruncateSummaryBoundary(t *testing.T) {
	at60 := strings.Repeat("x", 60)
	if got := truncateSummary(at60); got != at60 {
		t.Fatalf("60 runes must pass through, got %q", got)
	}
	at61 := strings.Repeat("x", 61)
	if got := truncateSummary(at61); got != at60+"…" {
		t.Fatalf("61 runes must clip to 60+ellipsis, got %q", got)
	}
	at62 := strings.Repeat("x", 62)
	if got := truncateSummary(at62); got != at60+"…" {
		t.Fatalf("62 runes must clip to 60+ellipsis, got %q", got)
	}
}

func TestNotice_DeferredWhileAssistantStreaming(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "answering out loud"}) // assistant streaming
	// A reminder arriving mid-stream must be held, not spliced into the answer.
	tr.Apply(shell3.Event{Kind: shell3.SystemReminder, Text: "<system-reminder>\nsubagent x finished\n</system-reminder>"})
	if last := tr.items[len(tr.items)-1]; last.Kind != ItemAssistant {
		t.Fatalf("reminder must not split the streaming answer; last item kind = %v", last.Kind)
	}
	// When the answer completes, the held reminder flushes — after the block.
	tr.Apply(shell3.Event{Kind: shell3.Done})
	if n := len(tr.items); n < 2 || tr.items[n-2].Kind != ItemAssistant || tr.items[n-1].Kind != ItemNotice {
		t.Fatalf("reminder should render right after the finished answer: %+v", tr.items)
	}
}

func TestFoldedToolShowsHeaderOnly(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "secret-input"})
	tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "secret-output"})
	out, _, _, _ := tr.renderBlocks(0, false, 80, -1, -1)
	plain := stripANSI(out)
	if strings.Contains(plain, "secret-output") {
		t.Fatalf("folded tool must not show output:\n%s", plain)
	}
	if !strings.Contains(plain, "bash") {
		t.Fatalf("folded tool should still show its header:\n%s", plain)
	}
}
