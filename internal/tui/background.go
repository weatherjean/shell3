package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// The background-jobs modal lists the live background jobs (bash_bg processes and
// fire-and-forget subagents), lets you open one job's output, and kill it. It is
// a two-pane overlay: a list view, and an output view for the selected job. All
// keys route here while it is open (see handleKey).

// bgModal is the background-jobs modal's state, operated on exclusively by this
// file (plus the mode dispatch in keys.go and the wheel routing in mouse.go).
type bgModal struct {
	open         bool
	jobs         []shell3.JobInfo
	sel          int      // selected row in the list view
	viewID       string   // non-empty = viewing this job's output (else the list)
	output       string   // loaded output of the viewed job (raw transcript JSONL or stdout)
	isTranscript bool     // true ⇒ output is a subagent --out transcript (render structured)
	scroll       int      // first visible output line in the output view
	notice       string   // status line shown inside the modal (e.g. a kill result)
	rows         []string // memoized rendered output rows (nil = recompute; see bgWrappedLines)
	rowsW        int      // content width rows was rendered at (re-render on resize)
}

// bgListMaxWidth / bgOutputMaxWidth cap the modal's content width in its two
// views; both views also shrink to 3/4 of the terminal (see modalWidth).
const (
	bgListMaxWidth   = 100
	bgOutputMaxWidth = 110
)

// bgModalChromeRows is the vertical chrome the modal reserves around its body
// (title, footer, blank separators, padding).
const bgModalChromeRows = 8

// jobRowMargin is the horizontal slack a job row reserves for the selection
// bar and panel padding.
const jobRowMargin = 6

// bgLiveTailCap is the maximum byte length of m.bg.output during a live raw-mode
// view. It matches the ring buffer size in jobs.go (newRingBuffer(64*1024)) so
// unbounded growth is impossible: when exceeded, the front is trimmed to keep the
// most-recent 64 KB — the same tail-keeping semantics the ring uses.
const bgLiveTailCap = 64 * 1024

// openBackground snapshots the live jobs and opens the modal in its list view.
func (m *model) openBackground() {
	if m.cmds == nil {
		m.notice = "no session"
		return
	}
	m.bg.jobs = m.cmds.Jobs()
	m.bg.sel = 0
	m.bg.viewID = ""
	m.bg.output = ""
	m.bg.isTranscript = false
	m.bg.rows = nil
	m.bg.scroll = 0
	m.bg.notice = ""
	m.bg.open = true
}

// loadJobOutput loads the viewed job's content into bgOutput: a subagent's
// structured --out transcript when it has one (rendered with thinking/tool/
// markdown styling), else the plain stdout log (wrapped). bgIsTranscript records
// which, so bgWrappedLines renders the right way.
func (m *model) loadJobOutput(id string) {
	if tr := m.cmds.JobTranscript(id); strings.TrimSpace(tr) != "" {
		m.bg.output, m.bg.isTranscript = tr, true
	} else {
		m.bg.output, m.bg.isTranscript = m.cmds.JobOutput(id), false
	}
	m.bg.rows = nil // new content → drop the memoized rows
}

// bgMaxScroll is the largest first-line offset that still fills the output view
// with the last screenful. Scrolling past it would shrink the visible output
// toward a single trailing line, so j / G / wheel-down all clamp here.
func (m *model) bgMaxScroll() int {
	if n := len(m.bgWrappedLines()) - m.bgModalHeight(); n > 0 {
		return n
	}
	return 0
}

// bgScrollToBottom positions the output view at its last screenful — the most
// recent output. It is the default when opening a job (the latest output is shown
// first; the view is a snapshot, refreshed with r, not a continuous tail) and the
// target of the G key.
func (m *model) bgScrollToBottom() { m.bg.scroll = m.bgMaxScroll() }

// handleBackgroundWheel scrolls the modal with the mouse wheel: the output view
// by 3 lines (clamped like j/k), the list view by moving the selection.
func (m *model) handleBackgroundWheel(e tea.Mouse) {
	if m.bg.viewID == "" {
		switch e.Button {
		case tea.MouseWheelUp:
			m.bg.sel = max(m.bg.sel-1, 0)
		case tea.MouseWheelDown:
			m.bg.sel = min(m.bg.sel+1, len(m.bg.jobs)-1)
		}
		return
	}
	switch e.Button {
	case tea.MouseWheelUp:
		m.bg.scroll = max(m.bg.scroll-3, 0)
	case tea.MouseWheelDown:
		m.bg.scroll = min(m.bg.scroll+3, m.bgMaxScroll())
	}
}

// selectedJobID is the id of the highlighted row, or "" when the list is empty.
func (m *model) selectedJobID() string {
	if m.bg.sel >= 0 && m.bg.sel < len(m.bg.jobs) {
		return m.bg.jobs[m.bg.sel].ID
	}
	return ""
}

// targetJobID is the job a kill acts on: the viewed job in the output view,
// otherwise the selected row.
func (m *model) targetJobID() string {
	if m.bg.viewID != "" {
		return m.bg.viewID
	}
	return m.selectedJobID()
}

// handleBackgroundKey drives the modal. It owns every key while open, so esc/q
// close it (and ctrl+c can't arm quit underneath).
func (m *model) handleBackgroundKey(s string) (tea.Model, tea.Cmd) {
	// Output view: esc returns to the list; j/k scroll; ctrl+x kills.
	if m.bg.viewID != "" {
		switch s {
		case "esc":
			m.bg.viewID = ""
			m.bg.scroll = 0
		case "q", "ctrl+c":
			m.bg.open = false
		case "ctrl+x":
			m.killTargetJob()
		case "r":
			m.loadJobOutput(m.bg.viewID)
			m.bgScrollToBottom() // live tail: refresh sticks to the bottom
			m.bg.notice = "refreshed"
		case "j", "down":
			m.bg.scroll = min(m.bg.scroll+1, m.bgMaxScroll())
		case "k", "up":
			m.bg.scroll = max(m.bg.scroll-1, 0)
		case "g":
			m.bg.scroll = 0
		case "G":
			m.bgScrollToBottom()
		}
		return m, nil
	}
	// List view.
	switch s {
	case "esc", "q", "ctrl+c":
		m.bg.open = false
	case "j", "down":
		m.bg.sel = min(m.bg.sel+1, len(m.bg.jobs)-1)
	case "k", "up":
		m.bg.sel = max(m.bg.sel-1, 0)
	case "enter", " ":
		if id := m.selectedJobID(); id != "" {
			m.bg.viewID = id
			m.loadJobOutput(id)
			m.bg.notice = ""
			m.bgScrollToBottom() // open at the bottom (most recent output)
		}
	case "ctrl+x":
		m.killTargetJob()
	case "r":
		m.refreshJobs()
	}
	return m, nil
}

// killTargetJob signals the targeted job, drops it from the displayed list
// optimistically (a stubborn job reappears on the next refresh), and returns to
// the list view.
func (m *model) killTargetJob() {
	id := m.targetJobID()
	if id == "" {
		return
	}
	if err := m.cmds.KillJob(id); err != nil {
		m.bg.notice = "kill failed: " + err.Error()
	} else {
		m.bg.notice = "sent SIGTERM to " + id
	}
	m.bg.jobs = slices.DeleteFunc(m.bg.jobs, func(j shell3.JobInfo) bool { return j.ID == id })
	m.clampJobSel()
	m.bg.viewID = ""
	m.bg.scroll = 0
}

// handleJobProgress processes one event from the job-progress bus. It keeps the
// bg:N footer pill up-to-date and, when the background modal is open and
// displaying the job's raw stdout view, live-appends the chunk so the user sees
// output stream in without pressing 'r'. Transcript views are left untouched
// (appending raw chunk text into structured JSONL would corrupt the render).
func (m *model) handleJobProgress(p shell3.JobProgress) {
	// Update the pill on Done events (running count may have changed).
	if p.Done && m.cmds != nil {
		jobs := m.cmds.Jobs()
		m.bgCount = countRunningJobs(jobs)
		if m.bg.open {
			m.bg.jobs = jobs
			m.clampJobSel()
			// If we were viewing this job in raw mode, reload its final output from
			// the ring buffer so the view is authoritative and complete.
			if m.bg.viewID == p.JobID && !m.bg.isTranscript {
				m.loadJobOutput(m.bg.viewID)
			}
		}
		return
	}
	// Live-append chunk only when the modal is open, viewing this job, in raw mode.
	if p.Chunk != "" && m.bg.open && m.bg.viewID == p.JobID && !m.bg.isTranscript {
		m.bg.output += p.Chunk
		// Cap to bgLiveTailCap by trimming from the front (tail-preserving, matching
		// the ring buffer semantics). Byte-trim is intentional — no rune/ANSI boundary
		// logic, consistent with how the ring itself trims.
		if len(m.bg.output) > bgLiveTailCap {
			m.bg.output = m.bg.output[len(m.bg.output)-bgLiveTailCap:]
		}
		// Invalidate the memoized row cache so bgWrappedLines recomputes on the
		// next render (same mechanism as loadJobOutput — nil bgRows triggers a reflow).
		m.bg.rows = nil
	}
}

// refreshJobs re-snapshots the live job list, clamping the selection.
func (m *model) refreshJobs() {
	m.bg.jobs = m.cmds.Jobs()
	m.clampJobSel()
	m.bg.notice = "refreshed"
}

func (m *model) clampJobSel() {
	m.bg.sel = max(min(m.bg.sel, len(m.bg.jobs)-1), 0)
}

// bgModalHeight is the number of content rows the modal body may use, leaving
// room for the title, footer, and a little breathing space.
func (m *model) bgModalHeight() int {
	return max(m.height-bgModalChromeRows, 3)
}

// backgroundBox renders the modal — the list view, or the output view when a
// job is selected.
func (m *model) backgroundBox() string {
	if m.bg.viewID != "" {
		return m.backgroundOutputBox()
	}
	w := m.modalWidth(m.width*3/4, bgListMaxWidth)
	rows := []string{stBrand.Render("background jobs"), ""}
	if len(m.bg.jobs) == 0 {
		rows = append(rows, stFgDim.Render("no background jobs"))
	} else {
		// Window the list around the selection so a long list never overflows.
		maxRows := m.bgModalHeight()
		start := 0
		if len(m.bg.jobs) > maxRows {
			start = min(max(m.bg.sel-maxRows/2, 0), len(m.bg.jobs)-maxRows)
		}
		end := min(start+maxRows, len(m.bg.jobs))
		for i := start; i < end; i++ {
			rows = append(rows, m.jobRow(m.bg.jobs[i], i == m.bg.sel, w))
		}
		if len(m.bg.jobs) > maxRows {
			rows = append(rows, stDim.Render(fmt.Sprintf("  %d of %d", m.bg.sel+1, len(m.bg.jobs))))
		}
	}
	if m.bg.notice != "" {
		rows = append(rows, "", stInfo.Render(m.bg.notice))
	}
	rows = append(rows, "", stDim.Render("j/k move · enter view · ctrl+x kill · r refresh · esc"))
	return modalBox(rows, 0, 1, w)
}

// jobRow renders one job line: a selection bar, id, age, pid, state, and
// label. Finished jobs show "✓ done" or "✗ error"; running jobs show nothing.
// Subagent jobs are prefixed with "@subagent"; nested jobs get an indented
// label with a depth marker (e.g. "  (depth 2)") so the user can see the
// nesting level at a glance.
func (m *model) jobRow(j shell3.JobInfo, selected bool, w int) string {
	bar := "  "
	if selected {
		bar = stBar.Render("▌") + " "
	}
	meta := stDim.Render(fmt.Sprintf("%s  %-4s pid %d  ", j.ID, shortAge(j.StartedAt), j.PID))

	var state string
	if j.Done {
		if j.Exit != nil && *j.Exit != 0 {
			state = stErr.Render("✗ error") + " "
		} else {
			state = stInfo.Render("✓ done") + " "
		}
	}

	label := strings.Join(strings.Fields(j.Cmd), " ")
	if j.Kind == shell3.JobSubagent {
		agent := j.Agent
		if agent == "" {
			agent = "subagent"
		}
		label = "@" + agent + " " + label
	}
	if j.Depth > 0 {
		label = strings.Repeat("  ", j.Depth) + label + fmt.Sprintf("  (depth %d)", j.Depth)
	}
	// Fit the whole row (bar + meta + state + label) within the box content
	// width, leaving jobRowMargin for the selection bar and padding.
	avail := max(w-lipgloss.Width(meta)-lipgloss.Width(state)-jobRowMargin, 8)
	return bar + meta + state + stUserText.Render(strutil.ClipRunes(label, avail))
}

// bgOutputBoxWidth is the output view's box width — the single source shared
// by the render (backgroundOutputBox) and the wrap/scroll math (bgOutputWidth
// → bgWrappedLines → bgMaxScroll). If these ever diverged, the scroll clamps
// would desync from what is drawn.
func (m *model) bgOutputBoxWidth() int {
	return m.modalWidth(m.width*3/4, bgOutputMaxWidth)
}

// bgOutputWidth is the wrapped content width of the output view — the box
// width minus modalBox's 2-col horizontal padding.
func (m *model) bgOutputWidth() int {
	return max(m.bgOutputBoxWidth()-2, 1)
}

// bgWrappedLines returns the display rows the output view scrolls over. For a
// subagent it renders the structured --out transcript (thinking, tool calls,
// markdown answer); for a plain bash_bg job it soft-wraps the stdout log to the
// content width (word-wrap, hard-breaking over-long words) so streamed prose
// stays readable instead of being truncated. Single source of truth for both the
// render (backgroundOutputBox) and the j/k/G scroll clamps (handleBackgroundKey).
func (m *model) bgWrappedLines() []string {
	// Memoize: this is called on every render frame and per scroll keypress, and
	// the transcript path runs glamour markdown — too costly to redo each frame.
	// Invalidated by content (loadJobOutput/openBackground nil bgRows) and by a
	// width change (resize).
	w := m.bgOutputWidth()
	if m.bg.rows != nil && m.bg.rowsW == w {
		return m.bg.rows
	}
	if m.bg.isTranscript {
		m.bg.rows = hardWrapRows(renderJobTranscript(m.bg.output, w), w)
	} else {
		wrapped := ansi.Wrap(strings.TrimRight(m.bg.output, "\n"), w, " ")
		m.bg.rows = strings.Split(wrapped, "\n")
	}
	m.bg.rowsW = w
	return m.bg.rows
}

// hardWrapRows guarantees every row is at most w columns by hard-breaking any
// that exceed it. The transcript markdown path (glamour) word-wraps but does NOT
// break an over-long token — a long URL, path, or hash in a subagent's answer can
// produce a row wider than w, which modalBox's fixed Width would then re-wrap into extra
// terminal rows, desyncing the one-row-per-element height cap and scroll math.
func hardWrapRows(rows []string, w int) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.Split(ansi.Hardwrap(r, w, true), "\n")...)
	}
	return out
}

// backgroundOutputBox renders a scrollable, soft-wrapped tail of the viewed
// job's output.
func (m *model) backgroundOutputBox() string {
	w := m.bgOutputBoxWidth()
	maxLines := m.bgModalHeight()
	lines := m.bgWrappedLines()
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		lines = []string{stFgDim.Render("(no output yet)")}
	}
	// Re-clamp the persisted scroll: a terminal resize changes bgMaxScroll without
	// a keypress, and could otherwise leave bgScroll past the new bottom — a short
	// final page until the next j/k. Clamp to the same bound the scroll keys use.
	m.bg.scroll = max(min(m.bg.scroll, m.bgMaxScroll()), 0)
	scroll := m.bg.scroll
	end := min(scroll+maxLines, len(lines))

	header := stBrand.Render("job ") + stInfo.Render(m.bg.viewID)
	if len(lines) > maxLines {
		header += stDim.Render(fmt.Sprintf("  (lines %d–%d of %d)", scroll+1, end, len(lines)))
	}
	footer := stDim.Render("j/k scroll · g/G top/bottom · ctrl+x kill · r refresh · esc back")
	// Header and footer are single logical rows. Truncate them (ANSI-aware) to the
	// content width so a terminal narrower than the footer hint can't make
	// modalBox's fixed Width re-wrap them into a second row — which would consume a body
	// row and desync the height budget bgModalHeight reserves.
	contentW := w - 2 // modalBox's horizontal padding
	header = ansi.Truncate(header, contentW, "…")
	footer = ansi.Truncate(footer, contentW, "…")
	body := make([]string, 0, end-scroll)
	body = append(body, lines[scroll:end]...) // already wrapped to the content width
	rows := append([]string{header, ""}, body...)
	if m.bg.notice != "" {
		rows = append(rows, "", ansi.Truncate(stInfo.Render(m.bg.notice), contentW, "…"))
	}
	rows = append(rows, "", footer)
	return modalBox(rows, 0, 1, w)
}

// shortAge formats a job's age compactly (e.g. "5s", "3m", "2h", "1d").
func shortAge(start time.Time) string {
	if start.IsZero() {
		return "?"
	}
	d := time.Since(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
