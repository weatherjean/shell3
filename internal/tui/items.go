package tui

import (
	"fmt"
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// ItemKind classifies a transcript item.
type ItemKind int

const (
	ItemUser ItemKind = iota
	ItemAssistant
	ItemReasoning
	ItemTool
	ItemNotice
)

// NoticeKind sub-classifies an ItemNotice.
type NoticeKind int

const (
	NoticeReminder NoticeKind = iota
	NoticeRetry
	NoticeCompacted
	NoticeError
	NoticeInfo // result of a : command
)

// Item is one transcript block. Tool and reasoning blocks are foldable.
type Item struct {
	Kind       ItemKind
	Text       string
	ToolName   string
	ToolInput  string
	ToolOutput string
	ToolCallID string
	ToolDone   bool
	ToolError  bool // the tool result was an error (renders ✗, not ✓)
	Notice     NoticeKind
	Folded     bool
	Steer      bool // a mid-turn steering message (user item, marked)

	// Cached glamour render of an assistant block, so refresh() (fired on every
	// key/scroll event) doesn't re-run the expensive markdown renderer when
	// nothing changed. Assistant text is append-only, so (width, len) is a sound
	// cache key — plus mdEpoch, which bumps on a palette switch so a light/dark
	// change recolors already-rendered blocks. Without this, mouse-scroll bursts
	// re-rendered every block per event and pegged the CPU.
	mdWidth int
	mdLen   int
	mdEpoch uint64
	mdOut   string
}

func (it *Item) foldable() bool {
	return it.Kind == ItemTool || it.Kind == ItemReasoning ||
		(it.Kind == ItemNotice && it.Notice == NoticeReminder)
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

func toolStyle(name string) lipgloss.Style { return toolRenderFor(name).style }

// Transcript folds the streamed event sequence into ordered items.
type Transcript struct {
	items          []*Item
	openAssistant  int
	openReasoning  int
	pendingNotices []*Item // reminder chrome held back while an assistant block streams (flushed on close)
}

func NewTranscript() *Transcript { return &Transcript{openAssistant: -1, openReasoning: -1} }

func (t *Transcript) AddUser(text string) {
	t.closeStreaming()
	t.items = append(t.items, &Item{Kind: ItemUser, Text: text})
}

// AddInfo appends a : command result as an info block.
func (t *Transcript) AddInfo(text string) {
	t.closeStreaming()
	t.items = append(t.items, &Item{Kind: ItemNotice, Notice: NoticeInfo, Text: text})
}

// AddCanceled appends a clean, dim "canceled" marker for a Ctrl+C-stopped turn
// (the raw context.Canceled error would otherwise render as a red ✗ error).
func (t *Transcript) AddCanceled() {
	t.closeStreaming()
	t.items = append(t.items, &Item{Kind: ItemNotice, Notice: NoticeReminder, Text: "⊘ canceled"})
}

// AddSteer appends a steering message the user sent mid-turn. It renders as a
// user prompt with a "steer" marker so it's distinguishable from a fresh turn.
func (t *Transcript) AddSteer(text string) {
	t.closeStreaming()
	t.items = append(t.items, &Item{Kind: ItemUser, Steer: true, Text: text})
}

// Apply folds one event in, returning true if the item list changed.
func (t *Transcript) Apply(ev shell3.Event) bool {
	switch ev.Kind {
	case shell3.Token:
		t.foldOpenReasoning() // thinking block (if any) is done → collapse it
		if t.openAssistant < 0 {
			t.items = append(t.items, &Item{Kind: ItemAssistant})
			t.openAssistant = len(t.items) - 1
		}
		t.items[t.openAssistant].Text += ev.Text
		return true
	case shell3.Reasoning:
		t.openAssistant = -1
		if t.openReasoning < 0 {
			// Show thinking live (unfolded) as it streams; foldOpenReasoning
			// collapses it once the block completes.
			t.items = append(t.items, &Item{Kind: ItemReasoning, Folded: false})
			t.openReasoning = len(t.items) - 1
		}
		t.items[t.openReasoning].Text += ev.Text
		return true
	case shell3.ToolCall:
		t.closeStreaming()
		t.items = append(t.items, &Item{Kind: ItemTool, ToolName: ev.ToolName, ToolInput: ev.ToolInput, ToolCallID: ev.ToolCallID, Folded: foldedByDefault(ev.ToolName)})
		return true
	case shell3.ToolResult:
		t.closeStreaming()
		if i := t.findOpenTool(ev.ToolCallID); i >= 0 {
			t.items[i].ToolOutput = ev.ToolOutput
			t.items[i].ToolDone = true
			t.items[i].ToolError = ev.ToolError
		} else {
			t.items = append(t.items, &Item{Kind: ItemTool, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput, ToolCallID: ev.ToolCallID, ToolDone: true, ToolError: ev.ToolError, Folded: foldedByDefault(ev.ToolName)})
		}
		return true
	case shell3.SystemReminder:
		return t.addNotice(NoticeReminder, ev.Text)
	case shell3.Compacted:
		return t.addNotice(NoticeCompacted, ev.Text)
	case shell3.Retry:
		return t.addNotice(NoticeRetry, ev.Text)
	case shell3.Error:
		msg := ""
		if ev.Err != nil {
			msg = ev.Err.Error()
		}
		return t.addNotice(NoticeError, msg)
	case shell3.Done:
		// Return true when closeStreaming flushes reminder chrome held back during
		// streaming, so the view refreshes to show it.
		hadPending := len(t.pendingNotices) > 0
		t.closeStreaming()
		return hadPending
	case shell3.Usage:
		return false
	}
	return false
}

func (t *Transcript) addNotice(kind NoticeKind, text string) bool {
	// System reminders start folded — they're frequent host chrome, shown as a
	// compact one-line indicator the user can expand.
	n := &Item{Kind: ItemNotice, Notice: kind, Text: text, Folded: kind == NoticeReminder}
	// A reminder must never split a streaming assistant answer. If one arrives
	// while the answer block is still open, hold it and flush it when the block
	// closes (closeStreaming) so it renders right after the finished answer. Only
	// NoticeReminder is held (host notifications, steering, context reminders);
	// errors/retries stay inline so they remain visible mid-stream.
	if kind == NoticeReminder && t.openAssistant >= 0 {
		t.pendingNotices = append(t.pendingNotices, n)
		return false
	}
	t.closeStreaming()
	t.items = append(t.items, n)
	return true
}

// reminderBody strips the <system-reminder> wrapper for display, leaving just the
// reminder content.
func reminderBody(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<system-reminder>")
	s = strings.TrimSuffix(s, "</system-reminder>")
	return strings.TrimSpace(s)
}

func (t *Transcript) closeStreaming() {
	t.foldOpenReasoning()
	// Drop a streaming assistant block that carries no visible content. Models
	// often emit a stray space/newline — or a leaked reasoning tag like </think>
	// (MiniMax) — into content right before a tool call; glamour renders it to
	// nothing, leaving an empty block that reads as a blank gap above the tool.
	// The open assistant is always the last item (only Token appends to it, and
	// every other event calls closeStreaming first), so it is safe to trim.
	if t.openAssistant >= 0 && t.openAssistant == len(t.items)-1 &&
		isBlankAssistant(t.items[t.openAssistant].Text) {
		t.items = t.items[:t.openAssistant]
	}
	t.openAssistant = -1
	// Flush reminder chrome held back while the answer streamed (addNotice), so it
	// renders right after the completed block instead of splitting it.
	if len(t.pendingNotices) > 0 {
		t.items = append(t.items, t.pendingNotices...)
		t.pendingNotices = nil
	}
}

// isBlankAssistant reports whether assistant text has no visible content once
// whitespace and leaked reasoning tags are removed. Such a block renders empty,
// so it is dropped rather than shown as a gap.
func isBlankAssistant(s string) bool {
	for _, tag := range []string{"<think>", "</think>", "<thinking>", "</thinking>"} {
		s = strings.ReplaceAll(s, tag, "")
	}
	return strings.TrimSpace(s) == ""
}

// foldOpenReasoning collapses the currently-streaming thinking block (if any)
// and marks it closed, so a finished thinking block shows as a folded summary.
func (t *Transcript) foldOpenReasoning() {
	if t.openReasoning >= 0 && t.openReasoning < len(t.items) {
		t.items[t.openReasoning].Folded = true
	}
	t.openReasoning = -1
}

func (t *Transcript) findOpenTool(callID string) int {
	for i := len(t.items) - 1; i >= 0; i-- {
		if it := t.items[i]; it.Kind == ItemTool && it.ToolCallID == callID && !it.ToolDone {
			return i
		}
	}
	return -1
}

// FoldAll sets the fold state on every foldable block.
func (t *Transcript) FoldAll(folded bool) {
	for _, it := range t.items {
		if it.foldable() {
			it.Folded = folded
		}
	}
}

// ToggleFold flips the fold of item i if foldable; returns true if it changed.
func (t *Transcript) ToggleFold(i int) bool {
	if i < 0 || i >= len(t.items) || !t.items[i].foldable() {
		return false
	}
	t.items[i].Folded = !t.items[i].Folded
	return true
}

func (t *Transcript) count() int { return len(t.items) }

// raw returns the unstyled text of item i for the clipboard.
func (t *Transcript) raw(i int) string {
	if i < 0 || i >= len(t.items) {
		return ""
	}
	it := t.items[i]
	if it.Kind == ItemTool {
		var b strings.Builder
		b.WriteString(it.ToolName)
		if it.ToolInput != "" {
			b.WriteString("\n" + it.ToolInput)
		}
		if it.ToolOutput != "" {
			b.WriteString("\n" + it.ToolOutput)
		}
		return b.String()
	}
	return it.Text
}

// renderBlocks renders all items to viewport content, wrapping each to width.
// In NORMAL mode the single line at cursorLine gets a left bar (the vim cursor).
// starts[i] is the first content line of item i; total is the line count. The
// caller maps cursorLine→block for folding/yank and scrolls to keep the cursor
// visible.
func (t *Transcript) renderBlocks(cursorLine int, normal bool, w int, selLo, selHi int) (content string, starts []int, total int, excluded []bool) {
	inner := w - 2
	if inner < 1 {
		inner = 1
	}
	wrap := lipgloss.NewStyle().Width(inner)
	var all []string
	// excluded is parallel to all: true = a meta line never highlighted (and so
	// never copied — selection copy consults the same mask).
	// A blank top margin so the first block doesn't sit flush against the
	// viewport's top edge; it scrolls with the content and never copies.
	all = append(all, "")
	excluded = append(excluded, true)
	starts = make([]int, len(t.items))
	for i, it := range t.items {
		starts[i] = len(all)
		var rendered string
		switch it.Kind {
		case ItemAssistant:
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
	// Clamp the cursor to a real line here so the bar renders on the right line
	// in THIS frame — even on the first render after entering NORMAL, where the
	// caller passes a sentinel (1<<30) meaning "last line" that it only clamps
	// against the total *after* this call. Without this, the bar would be missing
	// until the next refresh (i.e. only after the first action).
	cur := cursorLine
	if cur >= len(all) {
		cur = len(all) - 1
	}
	if cur < 0 {
		cur = 0
	}
	for idx := range all {
		bar := "  "
		if normal && idx == cur {
			bar = stBar.Render("▌") + " "
		}
		line := all[idx]
		if selLo <= selHi && idx >= selLo && idx <= selHi && !excluded[idx] {
			line = reverseContent(line, inner)
		}
		all[idx] = bar + line
	}
	return strings.Join(all, "\n"), starts, len(all), excluded
}

// metaExcluded reports whether a rendered line is meta chrome that mouse
// selection/copy should skip. isHeaderLine is true for the first line of the
// owning item. Injected system reminders are excluded entirely; a thinking
// (reasoning) block excludes only its "thinking" indicator line — the reasoning
// content below it stays selectable and copyable.
func metaExcluded(it *Item, isHeaderLine bool) bool {
	switch {
	case it.Kind == ItemNotice && it.Notice == NoticeReminder:
		return true
	case it.Kind == ItemReasoning:
		return isHeaderLine
	}
	return false
}

// reverseContent inverts the colors (AttrReverse) of a rendered line's content
// cells — the selection highlight. It parses the line (with its own ANSI) into an
// ultraviolet cell grid, so the highlight survives the SGR resets glamour bakes
// into colored content (a plain background style would be switched off mid-line
// by those resets). Only cells up to the last non-blank one are inverted, so
// trailing whitespace stays plain and an empty line (a block separator) is left
// untouched.
func reverseContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Size the cell grid to the wider of the viewport width and the line's own
	// content width: glamour markdown (e.g. long code lines) can exceed the
	// viewport, and a grid clipped to width would truncate both the highlight and
	// the text copied from it.
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

func renderItem(it *Item) string {
	chev := func(folded bool) string {
		if folded {
			return stChevron.Render("▸")
		}
		return stChevron.Render("▾")
	}
	switch it.Kind {
	case ItemUser:
		if it.Steer {
			return stThinking.Render("⤷ steer ") + stUserText.Render(it.Text)
		}
		return stUserPrompt.Render("› ") + stUserText.Render(it.Text)
	case ItemAssistant:
		return it.Text
	case ItemReasoning:
		if it.Folded {
			return chev(true) + " " + stThinking.Render(fmt.Sprintf("thinking (%d lines)", countLines(it.Text)))
		}
		return chev(false) + " " + stThinking.Render("thinking") + "\n" + stThinking.Render(strings.TrimRight(it.Text, "\n"))
	case ItemTool:
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
	case ItemNotice:
		switch it.Notice {
		case NoticeCompacted:
			return stBrand.Render("✦ conversation compacted")
		case NoticeError:
			return stErr.Render("✗ " + it.Text)
		case NoticeRetry:
			return stDim.Render("⟳ " + it.Text)
		case NoticeInfo:
			return stInfo.Render(it.Text)
		case NoticeReminder:
			// Render like a thinking block but in muted yellow, folded by default —
			// reminders are frequent host chrome, not content. Folded keeps them to a
			// one-line indicator instead of an invisible dim-gray gap.
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

func toolSummary(it *Item) string {
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

// truncateSummary clamps a one-line summary to the 60-rune card budget:
// anything over 60 runes becomes the first 60 plus an ellipsis. Not
// strutil.ClipRunes — that helper's budget includes the ellipsis (total ≤ n),
// which differs at exactly n runes.
func truncateSummary(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
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
