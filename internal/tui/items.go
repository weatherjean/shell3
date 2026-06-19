package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
	ItemBanner // session-start brand line, above the first user prompt
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
	Notice     NoticeKind
	Folded     bool
	Steer      bool // a mid-turn steering message (user item, marked)

	// Cached glamour render of an assistant block, so refresh() (fired on every
	// key/scroll event) doesn't re-run the expensive markdown renderer when
	// nothing changed. Assistant text is append-only, so (width, len) is a sound
	// cache key. Without this, mouse-scroll bursts re-rendered every block per
	// event and pegged the CPU.
	mdWidth int
	mdLen   int
	mdOut   string
}

func (it *Item) foldable() bool { return it.Kind == ItemTool || it.Kind == ItemReasoning }

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
	items         []*Item
	openAssistant int
	openReasoning int
}

func NewTranscript() *Transcript { return &Transcript{openAssistant: -1, openReasoning: -1} }

func (t *Transcript) AddUser(text string) {
	t.closeStreaming()
	// Keep the brand + phonetic as a one-line session-start marker above the
	// first prompt (the full welcome card only shows on an empty transcript).
	if len(t.items) == 0 {
		t.items = append(t.items, &Item{Kind: ItemBanner})
	}
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
		} else {
			t.items = append(t.items, &Item{Kind: ItemTool, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput, ToolCallID: ev.ToolCallID, ToolDone: true, Folded: foldedByDefault(ev.ToolName)})
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
		t.closeStreaming()
		return false
	case shell3.Usage:
		return false
	}
	return false
}

func (t *Transcript) addNotice(kind NoticeKind, text string) bool {
	t.closeStreaming()
	t.items = append(t.items, &Item{Kind: ItemNotice, Notice: kind, Text: text})
	return true
}

func (t *Transcript) closeStreaming() {
	t.foldOpenReasoning()
	t.openAssistant = -1
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
func (t *Transcript) renderBlocks(cursorLine int, normal bool, w int) (content string, starts []int, total int) {
	inner := w - 2
	if inner < 1 {
		inner = 1
	}
	wrap := lipgloss.NewStyle().Width(inner)
	var all []string
	starts = make([]int, len(t.items))
	for i, it := range t.items {
		starts[i] = len(all)
		var rendered string
		switch {
		case it.Kind == ItemBanner:
			rendered = renderBanner(inner)
		case it.Kind == ItemAssistant:
			// Assistant text is markdown (glamour wraps it itself, so bypass the
			// lipgloss wrapper that would mangle its ANSI). Cached by (width,len)
			// so a refresh that didn't change the text — e.g. scrolling — reuses
			// the render instead of re-running glamour.
			if it.mdOut == "" || it.mdWidth != inner || it.mdLen != len(it.Text) {
				it.mdOut = renderMarkdown(it.Text, inner)
				it.mdWidth = inner
				it.mdLen = len(it.Text)
			}
			rendered = it.mdOut
		default:
			rendered = wrap.Render(renderItem(it))
		}
		all = append(all, strings.Split(rendered, "\n")...)
		if i < len(t.items)-1 {
			all = append(all, "") // blank separator between blocks (not after the last)
		}
	}
	for idx := range all {
		bar := "  "
		if normal && idx == cursorLine {
			bar = stBar.Render("▌") + " "
		}
		all[idx] = bar + all[idx]
	}
	return strings.Join(all, "\n"), starts, len(all)
}

// renderBanner is the session-start "shell3" line. When width > 0 the middle is
// filled with dim "/" (matching the slash backgrounds) and a right-aligned hint
// reminds you to press esc to scroll the output.
func renderBanner(width int) string {
	head := stBrand.Render("๑ï shell3") + "  " + stDim.Render("/ˈʃɛli/")
	if width <= 0 {
		return head
	}
	hint := stDim.Render("esc to scroll output")
	used, hintW := lipgloss.Width(head), lipgloss.Width(hint)
	if gap := width - used - hintW - 2; gap >= 1 {
		return head + " " + stSlashBg.Render(strings.Repeat("/", gap)) + " " + hint
	}
	// Too narrow for the hint: just fill with slashes.
	if width > used+1 {
		return head + " " + stSlashBg.Render(strings.Repeat("/", width-used-1))
	}
	return head
}

func renderItem(it *Item) string {
	chev := func(folded bool) string {
		if folded {
			return stChevron.Render("▸")
		}
		return stChevron.Render("▾")
	}
	switch it.Kind {
	case ItemBanner:
		return renderBanner(0)
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
		return chev(false) + " " + stThinking.Render("thinking") + "\n" + stThinking.Render(indent(it.Text))
	case ItemTool:
		tr := toolRenderFor(it.ToolName)
		status := stDim.Render("…")
		if it.ToolDone {
			status = stTool.Render("✓")
		}
		head := chev(it.Folded) + " " + tr.style.Render("● "+it.ToolName)
		if it.Folded {
			return head + "  " + stDim.Render(toolSummary(it)) + "  " + status
		}
		var b strings.Builder
		b.WriteString(head + "  " + status)
		if !tr.hideInput && strings.TrimSpace(it.ToolInput) != "" {
			b.WriteString("\n" + stDim.Render(indent(it.ToolInput)))
		}
		if it.ToolDone && strings.TrimSpace(it.ToolOutput) != "" {
			out := it.ToolOutput
			if tr.colorize != nil {
				out = tr.colorize(out)
			}
			b.WriteString("\n" + indent(out))
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

func truncateSummary(s string) string {
	// Truncate by runes, not bytes, so a multibyte character is never split.
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

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "  " + ln
	}
	return strings.Join(lines, "\n")
}
