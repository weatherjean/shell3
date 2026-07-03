package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// The :background modal lists the live background jobs (bash_bg processes and
// fire-and-forget subagents), lets you open one job's output, and kill it. It is
// a two-pane overlay: a list view, and an output view for the selected job. All
// keys route here while it is open (see handleKey).

// bgLiveTailCap is the maximum byte length of m.bgOutput during a live raw-mode
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
	m.bgJobs = m.cmds.Jobs()
	m.bgSel = 0
	m.bgViewID = ""
	m.bgOutput = ""
	m.bgIsTranscript = false
	m.bgRows = nil
	m.bgScroll = 0
	m.bgNotice = ""
	m.bgOpen = true
}

// loadJobOutput loads the viewed job's content into bgOutput: a subagent's
// structured --out transcript when it has one (rendered with thinking/tool/
// markdown styling), else the plain stdout log (wrapped). bgIsTranscript records
// which, so bgWrappedLines renders the right way.
func (m *model) loadJobOutput(id string) {
	if tr := m.cmds.JobTranscript(id); strings.TrimSpace(tr) != "" {
		m.bgOutput, m.bgIsTranscript = tr, true
	} else {
		m.bgOutput, m.bgIsTranscript = m.cmds.JobOutput(id), false
	}
	m.bgRows = nil // new content → drop the memoized rows
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
func (m *model) bgScrollToBottom() { m.bgScroll = m.bgMaxScroll() }

// handleBackgroundWheel scrolls the modal with the mouse wheel: the output view
// by 3 lines (clamped like j/k), the list view by moving the selection.
func (m *model) handleBackgroundWheel(e tea.Mouse) {
	if m.bgViewID == "" {
		switch e.Button {
		case tea.MouseWheelUp:
			if m.bgSel > 0 {
				m.bgSel--
			}
		case tea.MouseWheelDown:
			if m.bgSel < len(m.bgJobs)-1 {
				m.bgSel++
			}
		}
		return
	}
	switch e.Button {
	case tea.MouseWheelUp:
		if m.bgScroll -= 3; m.bgScroll < 0 {
			m.bgScroll = 0
		}
	case tea.MouseWheelDown:
		if m.bgScroll += 3; m.bgScroll > m.bgMaxScroll() {
			m.bgScroll = m.bgMaxScroll()
		}
	}
}

// selectedJobID is the id of the highlighted row, or "" when the list is empty.
func (m *model) selectedJobID() string {
	if m.bgSel >= 0 && m.bgSel < len(m.bgJobs) {
		return m.bgJobs[m.bgSel].ID
	}
	return ""
}

// targetJobID is the job a kill acts on: the viewed job in the output view,
// otherwise the selected row.
func (m *model) targetJobID() string {
	if m.bgViewID != "" {
		return m.bgViewID
	}
	return m.selectedJobID()
}

// handleBackgroundKey drives the modal. It owns every key while open, so esc/q
// close it (and ctrl+c can't arm quit underneath).
func (m *model) handleBackgroundKey(s string) (tea.Model, tea.Cmd) {
	// Output view: esc returns to the list; j/k scroll; ctrl+x kills.
	if m.bgViewID != "" {
		switch s {
		case "esc":
			m.bgViewID = ""
			m.bgScroll = 0
		case "q", "ctrl+c":
			m.bgOpen = false
		case "ctrl+x":
			m.killTargetJob()
		case "r":
			m.loadJobOutput(m.bgViewID)
			m.bgScrollToBottom() // live tail: refresh sticks to the bottom
			m.bgNotice = "refreshed"
		case "j", "down":
			if m.bgScroll < m.bgMaxScroll() {
				m.bgScroll++
			}
		case "k", "up":
			if m.bgScroll > 0 {
				m.bgScroll--
			}
		case "g":
			m.bgScroll = 0
		case "G":
			m.bgScrollToBottom()
		}
		return m, nil
	}
	// List view.
	switch s {
	case "esc", "q", "ctrl+c":
		m.bgOpen = false
	case "j", "down":
		if m.bgSel < len(m.bgJobs)-1 {
			m.bgSel++
		}
	case "k", "up":
		if m.bgSel > 0 {
			m.bgSel--
		}
	case "enter", " ":
		if id := m.selectedJobID(); id != "" {
			m.bgViewID = id
			m.loadJobOutput(id)
			m.bgNotice = ""
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
		m.bgNotice = "kill failed: " + err.Error()
	} else {
		m.bgNotice = "sent SIGTERM to " + id
	}
	out := m.bgJobs[:0]
	for _, j := range m.bgJobs {
		if j.ID != id {
			out = append(out, j)
		}
	}
	m.bgJobs = out
	m.clampJobSel()
	m.bgViewID = ""
	m.bgScroll = 0
}

// handleJobProgress processes one event from the job-progress bus. It keeps the
// bg:N footer pill up-to-date and, when the :background modal is open and
// displaying the job's raw stdout view, live-appends the chunk so the user sees
// output stream in without pressing 'r'. Transcript views are left untouched
// (appending raw chunk text into structured JSONL would corrupt the render).
func (m *model) handleJobProgress(p shell3.JobProgress) tea.Cmd {
	// Update the pill on Done events (running count may have changed).
	if p.Done && m.cmds != nil {
		jobs := m.cmds.Jobs()
		m.bgCount = countRunningJobs(jobs)
		if m.bgOpen {
			m.bgJobs = jobs
			m.clampJobSel()
			// If we were viewing this job in raw mode, reload its final output from
			// the ring buffer so the view is authoritative and complete.
			if m.bgViewID == p.JobID && !m.bgIsTranscript {
				m.loadJobOutput(m.bgViewID)
			}
		}
		return nil
	}
	// Live-append chunk only when the modal is open, viewing this job, in raw mode.
	if p.Chunk != "" && m.bgOpen && m.bgViewID == p.JobID && !m.bgIsTranscript {
		m.bgOutput += p.Chunk
		// Cap to bgLiveTailCap by trimming from the front (tail-preserving, matching
		// the ring buffer semantics). Byte-trim is intentional — no rune/ANSI boundary
		// logic, consistent with how the ring itself trims.
		if len(m.bgOutput) > bgLiveTailCap {
			m.bgOutput = m.bgOutput[len(m.bgOutput)-bgLiveTailCap:]
		}
		// Invalidate the memoized row cache so bgWrappedLines recomputes on the
		// next render (same mechanism as loadJobOutput — nil bgRows triggers a reflow).
		m.bgRows = nil
	}
	return nil
}

// refreshJobs re-snapshots the live job list, clamping the selection.
func (m *model) refreshJobs() {
	m.bgJobs = m.cmds.Jobs()
	m.clampJobSel()
	m.bgNotice = "refreshed"
}

func (m *model) clampJobSel() {
	if m.bgSel >= len(m.bgJobs) {
		m.bgSel = len(m.bgJobs) - 1
	}
	if m.bgSel < 0 {
		m.bgSel = 0
	}
}

// bgModalHeight is the number of content rows the modal body may use, leaving
// room for the title, footer, and a little breathing space.
func (m *model) bgModalHeight() int {
	h := m.height - 8
	if h < 3 {
		h = 3
	}
	return h
}

// backgroundBox renders the modal — the list view, or the output view when a
// job is selected.
func (m *model) backgroundBox() string {
	if m.bgViewID != "" {
		return m.backgroundOutputBox()
	}
	w := m.modalWidth(m.width*3/4, 100)
	rows := []string{stBrand.Render("background jobs"), ""}
	if len(m.bgJobs) == 0 {
		rows = append(rows, stFgDim.Render("no background jobs"))
	} else {
		// Window the list around the selection so a long list never overflows.
		maxRows := m.bgModalHeight()
		start := 0
		if len(m.bgJobs) > maxRows {
			start = m.bgSel - maxRows/2
			if start < 0 {
				start = 0
			}
			if start > len(m.bgJobs)-maxRows {
				start = len(m.bgJobs) - maxRows
			}
		}
		end := start + maxRows
		if end > len(m.bgJobs) {
			end = len(m.bgJobs)
		}
		for i := start; i < end; i++ {
			rows = append(rows, m.jobRow(m.bgJobs[i], i == m.bgSel, w))
		}
		if len(m.bgJobs) > maxRows {
			rows = append(rows, stDim.Render(fmt.Sprintf("  %d of %d", m.bgSel+1, len(m.bgJobs))))
		}
	}
	if m.bgNotice != "" {
		rows = append(rows, "", stInfo.Render(m.bgNotice))
	}
	rows = append(rows, "", stDim.Render("j/k move · enter view · ctrl+x kill · r refresh · esc"))
	return bgPanel(w).Render(strings.Join(rows, "\n"))
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
		label = "@subagent " + label
	}
	if j.Depth > 0 {
		label = strings.Repeat("  ", j.Depth) + label + fmt.Sprintf("  (depth %d)", j.Depth)
	}
	// Fit the whole row (bar + meta + state + label) within the box content
	// width, leaving margin for the selection bar and padding.
	avail := w - lipgloss.Width(meta) - lipgloss.Width(state) - 6
	if avail < 8 {
		avail = 8
	}
	return bar + meta + state + stUserText.Render(clip(label, avail))
}

// clip truncates s to at most n runes, appending an ellipsis when it overflows.
func clip(s string, n int) string {
	if n < 1 {
		n = 1
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// bgOutputWidth is the wrapped content width of the output view — the modal
// width minus the 2-col horizontal padding.
func (m *model) bgOutputWidth() int {
	cw := m.modalWidth(m.width*3/4, 110) - 2
	if cw < 1 {
		cw = 1
	}
	return cw
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
	if m.bgRows != nil && m.bgRowsW == w {
		return m.bgRows
	}
	if m.bgIsTranscript {
		m.bgRows = hardWrapRows(renderJobTranscript(m.bgOutput, w), w)
	} else {
		wrapped := ansi.Wrap(strings.TrimRight(m.bgOutput, "\n"), w, " ")
		m.bgRows = strings.Split(wrapped, "\n")
	}
	m.bgRowsW = w
	return m.bgRows
}

// hardWrapRows guarantees every row is at most w columns by hard-breaking any
// that exceed it. The transcript markdown path (glamour) word-wraps but does NOT
// break an over-long token — a long URL, path, or hash in a subagent's answer can
// produce a row wider than w, which bgPanel's Width would then re-wrap into extra
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
	w := m.modalWidth(m.width*3/4, 110)
	maxLines := m.bgModalHeight()
	lines := m.bgWrappedLines()
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		lines = []string{stFgDim.Render("(no output yet)")}
	}
	// Re-clamp the persisted scroll: a terminal resize changes bgMaxScroll without
	// a keypress, and could otherwise leave bgScroll past the new bottom — a short
	// final page until the next j/k. Clamp to the same bound the scroll keys use.
	if maxScroll := m.bgMaxScroll(); m.bgScroll > maxScroll {
		m.bgScroll = maxScroll
	}
	if m.bgScroll < 0 {
		m.bgScroll = 0
	}
	scroll := m.bgScroll
	end := scroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}

	header := stBrand.Render("job ") + stInfo.Render(m.bgViewID)
	if len(lines) > maxLines {
		header += stDim.Render(fmt.Sprintf("  (lines %d–%d of %d)", scroll+1, end, len(lines)))
	}
	footer := stDim.Render("j/k scroll · g/G top/bottom · ctrl+x kill · r refresh · esc back")
	// Header and footer are single logical rows. Truncate them (ANSI-aware) to the
	// content width so a terminal narrower than the footer hint can't make
	// bgPanel.Width re-wrap them into a second row — which would consume a body
	// row and desync the height budget bgModalHeight reserves.
	contentW := w - 2 // bgPanel's horizontal padding
	header = ansi.Truncate(header, contentW, "…")
	footer = ansi.Truncate(footer, contentW, "…")
	body := make([]string, 0, end-scroll)
	body = append(body, lines[scroll:end]...) // already wrapped to the content width
	rows := append([]string{header, ""}, body...)
	if m.bgNotice != "" {
		rows = append(rows, "", ansi.Truncate(stInfo.Render(m.bgNotice), contentW, "…"))
	}
	rows = append(rows, "", footer)
	return bgPanel(w).Render(strings.Join(rows, "\n"))
}

func bgPanel(w int) lipgloss.Style {
	return lipgloss.NewStyle().
		Padding(0, 1).
		Width(w)
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
