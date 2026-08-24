package askui

import (
	"fmt"
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/weatherjean/shell3/internal/shell3"
)

// itemKind classifies a transcript item.
type itemKind int

const (
	itemUser itemKind = iota
	itemAssistant
	itemReasoning
	itemTool
	itemNotice
)

// noticeKind sub-classifies an itemNotice.
type noticeKind int

const (
	noticeReminder noticeKind = iota
	noticeRetry
	noticeCompacted
	noticeError
	noticeInfo // host chrome: resume marker, job waits, config warnings
)

// item is one transcript block. Tool and reasoning blocks are foldable.
type item struct {
	Kind       itemKind
	Text       string
	ToolName   string
	ToolInput  string
	ToolOutput string
	ToolCallID string
	ToolDone   bool
	ToolError  bool // the tool result was an error (renders ✗, not ✓)
	Notice     noticeKind
	Folded     bool
	Steer      bool // a mid-turn steering message (user item, marked)

	// Cached glamour render of an assistant block, so refresh() (fired on every
	// key/scroll event) doesn't re-run the expensive markdown renderer when
	// nothing changed. Assistant text is append-only, so (width, len) is a sound
	// cache key — plus mdEpoch, which bumps on a palette switch so a light/dark
	// change recolors already-rendered blocks.
	mdWidth int
	mdLen   int
	mdEpoch uint64
	mdOut   string
}

func (it *item) foldable() bool {
	return it.Kind == itemTool || it.Kind == itemReasoning ||
		(it.Kind == itemNotice && it.Notice == noticeReminder)
}

// toolRender describes how a tool's block is presented. A richly-rendered tool
// is one table entry rather than edits scattered across several functions.
type toolRender struct {
	style          lipgloss.Style
	expand         bool                // start unfolded
	hideInput      bool                // suppress the raw args
	colorize       func(string) string // output colorizer (nil = plain)
	summaryFromOut bool                // summarize from the first output line
}

// toolRenderFor returns the presentation for a tool: bash green, bash_bg red,
// edit_file yellow + expanded + diff-colorized + args hidden, everything else
// pink.
func toolRenderFor(name string) toolRender {
	switch name {
	case "bash":
		return toolRender{style: stToolBash}
	case "bash_bg":
		return toolRender{style: stToolBg}
	case "edit_file":
		return toolRender{style: stToolEdit, expand: true, hideInput: true, colorize: colorizeDiff, summaryFromOut: true}
	default:
		return toolRender{style: stToolOther}
	}
}

func foldedByDefault(toolName string) bool { return !toolRenderFor(toolName).expand }

// transcript folds the streamed event sequence into ordered items.
type transcript struct {
	items          []*item
	openAssistant  int
	openReasoning  int
	pendingNotices []*item // reminder chrome held back while an assistant block streams (flushed on close)
}

func newTranscript() *transcript { return &transcript{openAssistant: -1, openReasoning: -1} }

func (t *transcript) addUser(text string) {
	t.closeStreaming()
	t.items = append(t.items, &item{Kind: itemUser, Text: text})
}

// addInfo appends a host-chrome line (resume marker, background-job wait,
// config warning) as an info block.
func (t *transcript) addInfo(text string) {
	t.closeStreaming()
	t.items = append(t.items, &item{Kind: itemNotice, Notice: noticeInfo, Text: text})
}

// addCanceled appends a clean, dim "canceled" marker for a Ctrl+C-stopped turn
// (the raw context.Canceled error would otherwise render as a red ✗ error).
func (t *transcript) addCanceled() {
	t.closeStreaming()
	t.items = append(t.items, &item{Kind: itemNotice, Notice: noticeReminder, Text: "⊘ canceled"})
}

// addSteer appends a steering message the user sent mid-turn. It renders as a
// user prompt with a "steer" marker so it's distinguishable from a fresh turn.
func (t *transcript) addSteer(text string) {
	t.closeStreaming()
	t.items = append(t.items, &item{Kind: itemUser, Steer: true, Text: text})
}

// apply folds one event in, returning true if the item list changed.
func (t *transcript) apply(ev shell3.Event) bool {
	switch ev.Kind {
	case shell3.Token:
		t.foldOpenReasoning() // thinking block (if any) is done → collapse it
		if t.openAssistant < 0 {
			t.items = append(t.items, &item{Kind: itemAssistant})
			t.openAssistant = len(t.items) - 1
		}
		t.items[t.openAssistant].Text += ev.Text
		return true
	case shell3.Reasoning:
		t.openAssistant = -1
		if t.openReasoning < 0 {
			// Show thinking live (unfolded) as it streams; foldOpenReasoning
			// collapses it once the block completes.
			t.items = append(t.items, &item{Kind: itemReasoning, Folded: false})
			t.openReasoning = len(t.items) - 1
		}
		t.items[t.openReasoning].Text += ev.Text
		return true
	case shell3.ToolCall:
		t.closeStreaming()
		t.items = append(t.items, &item{Kind: itemTool, ToolName: ev.ToolName, ToolInput: ev.ToolInput, ToolCallID: ev.ToolCallID, Folded: foldedByDefault(ev.ToolName)})
		return true
	case shell3.ToolResult:
		t.closeStreaming()
		if i := t.findOpenTool(ev.ToolCallID); i >= 0 {
			t.items[i].ToolOutput = ev.ToolOutput
			t.items[i].ToolDone = true
			t.items[i].ToolError = ev.ToolError
		} else {
			t.items = append(t.items, &item{Kind: itemTool, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput, ToolCallID: ev.ToolCallID, ToolDone: true, ToolError: ev.ToolError, Folded: foldedByDefault(ev.ToolName)})
		}
		return true
	case shell3.SystemReminder:
		return t.addNotice(noticeReminder, ev.Text)
	case shell3.Compacted:
		return t.addNotice(noticeCompacted, ev.Text)
	case shell3.Retry:
		return t.addNotice(noticeRetry, ev.Text)
	case shell3.Error:
		msg := ""
		if ev.Err != nil {
			msg = ev.Err.Error()
			if h := shell3.RecoveryHint(ev.Err); h != "" {
				msg += "\n" + h
			}
		}
		return t.addNotice(noticeError, msg)
	case shell3.Done:
		// Return true when closeStreaming flushes reminder chrome held back
		// during streaming, so the view refreshes to show it.
		hadPending := len(t.pendingNotices) > 0
		t.closeStreaming()
		return hadPending
	case shell3.Usage:
		return false
	}
	return false
}

func (t *transcript) addNotice(kind noticeKind, text string) bool {
	// System reminders start folded — they're frequent host chrome, shown as a
	// compact one-line indicator the user can expand.
	n := &item{Kind: itemNotice, Notice: kind, Text: text, Folded: kind == noticeReminder}
	// A reminder must never split a streaming assistant answer. If one arrives
	// while the answer block is still open, hold it and flush it when the block
	// closes (closeStreaming) so it renders right after the finished answer.
	// Only noticeReminder is held; errors/retries stay inline so they remain
	// visible mid-stream.
	if kind == noticeReminder && t.openAssistant >= 0 {
		t.pendingNotices = append(t.pendingNotices, n)
		return false
	}
	t.closeStreaming()
	t.items = append(t.items, n)
	return true
}

// reminderBody strips the <system-reminder> wrapper for display, leaving just
// the reminder content.
func reminderBody(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<system-reminder>")
	s = strings.TrimSuffix(s, "</system-reminder>")
	return strings.TrimSpace(s)
}

func (t *transcript) closeStreaming() {
	t.foldOpenReasoning()
	// Drop a streaming assistant block that carries no visible content. Models
	// often emit a stray space/newline into content right before a tool call;
	// glamour renders it to nothing, leaving an empty block that reads as a
	// blank gap above the tool. The open assistant is always the last item (only
	// Token appends to it, and every other event calls closeStreaming first), so
	// it is safe to trim.
	if t.openAssistant >= 0 && t.openAssistant == len(t.items)-1 &&
		strings.TrimSpace(t.items[t.openAssistant].Text) == "" {
		t.items = t.items[:t.openAssistant]
	}
	t.openAssistant = -1
	// Flush reminder chrome held back while the answer streamed (addNotice), so
	// it renders right after the completed block instead of splitting it.
	if len(t.pendingNotices) > 0 {
		t.items = append(t.items, t.pendingNotices...)
		t.pendingNotices = nil
	}
}

// foldOpenReasoning collapses the currently-streaming thinking block (if any)
// and marks it closed, so a finished thinking block shows as a folded summary.
func (t *transcript) foldOpenReasoning() {
	if t.openReasoning >= 0 && t.openReasoning < len(t.items) {
		t.items[t.openReasoning].Folded = true
	}
	t.openReasoning = -1
}

func (t *transcript) findOpenTool(callID string) int {
	for i := len(t.items) - 1; i >= 0; i-- {
		if it := t.items[i]; it.Kind == itemTool && it.ToolCallID == callID && !it.ToolDone {
			return i
		}
	}
	return -1
}

// foldAll sets the fold state on every foldable block.
func (t *transcript) foldAll(folded bool) {
	for _, it := range t.items {
		if it.foldable() {
			it.Folded = folded
		}
	}
}

// toggleFold flips the fold of item i if foldable; returns true if it changed.
// Click-to-fold is the per-block path the keyboard deliberately does not have
// (ctrl+o is all-or-nothing).
func (t *transcript) toggleFold(i int) bool {
	if i < 0 || i >= len(t.items) || !t.items[i].foldable() {
		return false
	}
	t.items[i].Folded = !t.items[i].Folded
	return true
}

// anyUnfolded reports whether at least one foldable block is currently open —
// what ctrl+o toggles against, so the first press always collapses a noisy
// transcript rather than expanding it further.
func (t *transcript) anyUnfolded() bool {
	for _, it := range t.items {
		if it.foldable() && !it.Folded {
			return true
		}
	}
	return false
}

func (t *transcript) count() int { return len(t.items) }

// renderBlocks renders all items to viewport content, wrapping each to width.
// starts[i] is the first content line of item i; total is the line count. The
// caller maps a mouse Y to a block (via starts) for click-to-fold and drag
// selection. Lines in selLo..selHi are highlighted unless excluded.
func (t *transcript) renderBlocks(w int, selLo, selHi int) (content string, starts []int, total int, excluded []bool) {
	inner := w - 2
	if inner < 1 {
		inner = 1
	}
	wrap := lipgloss.NewStyle().Width(inner)
	// excluded is parallel to the rendered lines: true = a meta line never
	// highlighted (and so never copied — selection copy consults the same mask).
	// A blank top margin so the first block doesn't sit flush against the
	// viewport's top edge; it scrolls with the content and never copies.
	all := []string{""}
	excluded = []bool{true}
	starts = make([]int, len(t.items))
	for i, it := range t.items {
		starts[i] = len(all)
		var rendered string
		switch it.Kind {
		case itemAssistant:
			// Assistant text is markdown (glamour wraps it itself, so bypass the
			// lipgloss wrapper that would mangle its ANSI). Cached by (width,len)
			// so a refresh that didn't change the text — e.g. scrolling — reuses
			// the render instead of re-running glamour.
			if it.mdOut == "" || it.mdWidth != inner || it.mdLen != len(it.Text) || it.mdEpoch != mdEpoch {
				it.mdOut = renderMarkdown(it.Text, inner)
				it.mdWidth = inner
				it.mdLen = len(it.Text)
				it.mdEpoch = mdEpoch
			}
			rendered = it.mdOut
		default:
			rendered = wrap.Render(renderItem(it))
		}
		lines := strings.Split(rendered, "\n")
		all = append(all, lines...)
		for li := range lines {
			excluded = append(excluded, metaExcluded(it, li == 0))
		}
		if i < len(t.items)-1 {
			all = append(all, "") // blank separator between blocks (not after the last)
			excluded = append(excluded, true)
		}
	}
	for idx := range all {
		line := all[idx]
		if selLo <= selHi && idx >= selLo && idx <= selHi && !excluded[idx] {
			line = reverseContent(line, inner)
		}
		all[idx] = "  " + line // 2-col gutter (selectedText strips it)
	}
	return strings.Join(all, "\n"), starts, len(all), excluded
}

// metaExcluded reports whether a rendered line is meta chrome that selection
// and copy should skip. isHeaderLine is true for the first line of the owning
// item. Injected system reminders are excluded entirely; a thinking block
// excludes only its indicator line — the reasoning content below it stays
// selectable and copyable.
func metaExcluded(it *item, isHeaderLine bool) bool {
	switch {
	case it.Kind == itemNotice && it.Notice == noticeReminder:
		return true
	case it.Kind == itemReasoning:
		return isHeaderLine
	}
	return false
}

// reverseContent inverts the colors (AttrReverse) of a rendered line's content
// cells — the selection highlight. It parses the line (with its own ANSI) into
// an ultraviolet cell grid, so the highlight survives the SGR resets glamour
// bakes into colored content (a plain background style would be switched off
// mid-line by those resets). Only cells up to the last non-blank one are
// inverted, so trailing whitespace stays plain and an empty line (a block
// separator) is left untouched.
func reverseContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Size the cell grid to the wider of the viewport width and the line's own
	// content width: glamour markdown (e.g. long code lines) can exceed the
	// viewport, and a grid clipped to width would truncate both the highlight
	// and the text copied from it.
	w := width
	if cw := lipgloss.Width(s); cw > w {
		w = cw
	}
	buf := uv.NewScreenBuffer(w, 1)
	uv.NewStyledString(s).Draw(&buf, image.Rect(0, 0, w, 1))
	line := buf.Line(0)
	last := -1
	for x := 0; x < w; x++ {
		if c := line.At(x); c != nil && c.Content != "" && c.Content != " " {
			last = x
		}
	}
	for x := 0; x <= last; x++ {
		if c := line.At(x); c != nil {
			c.Style.Attrs |= uv.AttrReverse
		}
	}
	return strings.TrimRight(line.Render(), "\n")
}

func renderItem(it *item) string {
	chev := func(folded bool) string {
		if folded {
			return stChevron.Render("▸")
		}
		return stChevron.Render("▾")
	}
	switch it.Kind {
	case itemUser:
		if it.Steer {
			return stThinking.Render("⤷ steer ") + stUserText.Render(it.Text)
		}
		return stUserPrompt.Render("› ") + stUserText.Render(it.Text)
	case itemAssistant:
		return it.Text
	case itemReasoning:
		if it.Folded {
			return chev(true) + " " + stThinking.Render(fmt.Sprintf("thinking (%d lines)", countLines(it.Text)))
		}
		return chev(false) + " " + stThinking.Render("thinking") + "\n" + stThinking.Render(strings.TrimRight(it.Text, "\n"))
	case itemTool:
		tr := toolRenderFor(it.ToolName)
		status := stDim.Render("…")
		if it.ToolDone {
			if it.ToolError {
				status = stErr.Render("✗")
			} else {
				status = stTool.Render("✓")
			}
		}
		head := chev(it.Folded) + " " + tr.style.Render("● "+it.ToolName)
		if it.Folded {
			return head + "  " + stDim.Render(toolSummary(it)) + "  " + status
		}
		var b strings.Builder
		b.WriteString(head + "  " + status)
		if !tr.hideInput && strings.TrimSpace(it.ToolInput) != "" {
			b.WriteString("\n" + stDim.Render(strings.TrimRight(it.ToolInput, "\n")))
		}
		if it.ToolDone && strings.TrimSpace(it.ToolOutput) != "" {
			out := it.ToolOutput
			if tr.colorize != nil {
				out = tr.colorize(out)
			}
			b.WriteString("\n" + strings.TrimRight(out, "\n"))
		}
		return b.String()
	case itemNotice:
		switch it.Notice {
		case noticeCompacted:
			return stBrand.Render("✦ conversation compacted")
		case noticeError:
			return stErr.Render("✗ " + it.Text)
		case noticeRetry:
			return stDim.Render("⟳ " + it.Text)
		case noticeInfo:
			return stInfo.Render(it.Text)
		case noticeReminder:
			// Render like a thinking block but muted, folded by default —
			// reminders are frequent host chrome, not content. Folded keeps them
			// to a one-line indicator instead of an invisible dim-gray gap.
			body := reminderBody(it.Text)
			if it.Folded {
				return chev(true) + " " + stReminder.Render(fmt.Sprintf("reminder (%d lines)", countLines(body)))
			}
			return chev(false) + " " + stReminder.Render("reminder") + "\n" + stReminder.Render(body)
		default:
			return stDim.Render(it.Text)
		}
	}
	return ""
}

// colorizeDiff renders a unified diff (edit_file output) with git-diff-style
// add/remove/meta backgrounds and dimmed context.
func colorizeDiff(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			lines[i] = stDiffAdd.Render(l)
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			lines[i] = stDiffDel.Render(l)
		case strings.HasPrefix(l, "@@ "):
			lines[i] = stDiffMeta.Render(l)
		case l != "":
			lines[i] = stDim.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

func toolSummary(it *item) string {
	// For edit_file the first output line ("Edited <path> (+x -y …)") is a far
	// better one-liner than its JSON args.
	if toolRenderFor(it.ToolName).summaryFromOut && strings.TrimSpace(it.ToolOutput) != "" {
		first := strings.SplitN(strings.TrimSpace(it.ToolOutput), "\n", 2)[0]
		return truncateSummary(first)
	}
	s := strings.Join(strings.Fields(it.ToolInput), " ")
	if s == "" {
		s = strings.Join(strings.Fields(it.ToolOutput), " ")
	}
	return truncateSummary(s)
}

// summaryBudget is the folded block summary's rune budget (ellipsis excluded).
const summaryBudget = 60

// truncateSummary clamps a one-line summary to summaryBudget runes: anything
// over becomes the first summaryBudget runes plus an ellipsis.
func truncateSummary(s string) string {
	if r := []rune(s); len(r) > summaryBudget {
		return string(r[:summaryBudget]) + "…"
	}
	return s
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
