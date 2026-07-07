package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/weatherjean/shell3/internal/shell3"
)

func bgModel(jobs []shell3.JobInfo, out map[string]string) (*model, *fakeCmds) {
	fc := &fakeCmds{jobs: jobs, jobOut: out}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, fc
}

func TestBackground_OpensListsAndCloses(t *testing.T) {
	m, _ := bgModel([]shell3.JobInfo{
		{ID: "bg_aaa", Cmd: "go test ./...", PID: 111},
		{ID: "bg_bbb", Cmd: "sleep 999", PID: 222},
	}, nil)
	m.openBackground()
	if !m.bg.open || len(m.bg.jobs) != 2 {
		t.Fatalf("openBackground should list jobs: open=%v n=%d", m.bg.open, len(m.bg.jobs))
	}
	// The snapshot's modal enum/selection should agree with the raw state.
	if snap := m.uiSnapshot(); snap.modal != modalBackground || snap.modalSel != 0 {
		t.Fatalf("snapshot should report modalBackground with row 0 selected, got kind=%v sel=%d", snap.modal, snap.modalSel)
	}
	plain := frame(m)
	for _, want := range []string{"background jobs", "bg_aaa", "go test", "bg_bbb"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("modal missing %q in:\n%s", want, plain)
		}
	}
	frame(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.bg.open {
		t.Fatal("esc should close the :background modal")
	}
	if snap := m.uiSnapshot(); snap.modal != modalNone {
		t.Fatalf("snapshot should report modalNone once closed, got %v", snap.modal)
	}
}

func TestBackground_NavigateAndViewOutput(t *testing.T) {
	m, _ := bgModel([]shell3.JobInfo{
		{ID: "bg_aaa", Cmd: "a", PID: 1},
		{ID: "bg_bbb", Cmd: "b", PID: 2},
	}, map[string]string{"bg_bbb": "hello from bbb\nsecond line"})
	m.openBackground()
	m.Update(keyRune('j')) // move selection to the second job
	if m.bg.sel != 1 {
		t.Fatalf("j should move selection to 1, got %d", m.bg.sel)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // open its output
	if m.bg.viewID != "bg_bbb" {
		t.Fatalf("enter should open the selected job's output, got %q", m.bg.viewID)
	}
	if m.bg.isTranscript {
		t.Fatal("command job output should not use the transcript branch")
	}
	if !strings.Contains(stripANSI(m.View().Content), "hello from bbb") {
		t.Fatalf("output view should show the job log:\n%s", stripANSI(m.View().Content))
	}
	// esc returns to the list rather than closing the modal.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.bg.viewID != "" || !m.bg.open {
		t.Fatalf("esc in output view should return to the list: viewID=%q open=%v", m.bg.viewID, m.bg.open)
	}
}

func TestBackground_CtrlXKillsSelectedAndDrops(t *testing.T) {
	m, fc := bgModel([]shell3.JobInfo{
		{ID: "bg_aaa", Cmd: "a", PID: 1},
		{ID: "bg_bbb", Cmd: "b", PID: 2},
	}, nil)
	m.openBackground()
	m.Update(keyRune('j')) // select bg_bbb
	m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if len(fc.killed) != 1 || fc.killed[0] != "bg_bbb" {
		t.Fatalf("ctrl+x should kill the selected job, killed=%v", fc.killed)
	}
	for _, j := range m.bg.jobs {
		if j.ID == "bg_bbb" {
			t.Fatal("a killed job should be dropped from the displayed list")
		}
	}
}

func TestBackground_KillFailureShowsNotice(t *testing.T) {
	m, fc := bgModel([]shell3.JobInfo{{ID: "bg_aaa", Cmd: "a", PID: 1}}, nil)
	fc.killErr = fmt.Errorf("process gone")
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if !strings.Contains(m.bg.notice, "kill failed") || !strings.Contains(m.bg.notice, "process gone") {
		t.Fatalf("a failed kill should surface a notice, got %q", m.bg.notice)
	}
}

// On a terminal narrower than the footer hint, the output-view chrome (header +
// footer) must stay single rows — otherwise bgPanel.Width re-wraps them, adding
// rows that overflow the reserved modal height. With a one-line body the box is
// exactly header + blank + body + blank + footer = 5 rows.
func TestBackground_OutputChromeFitsNarrowTerminal(t *testing.T) {
	m, _ := bgModel([]shell3.JobInfo{{ID: "bg_x", Cmd: "sleep", PID: 1}}, map[string]string{"bg_x": "hi"})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 24}) // footer hint is wider than this modal
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	box := m.backgroundOutputBox()
	if rows := strings.Count(box, "\n") + 1; rows != 5 {
		t.Fatalf("output box should be 5 rows (chrome single-line), got %d:\n%s", rows, box)
	}
}

func TestBackground_OutputWrapsLongLines(t *testing.T) {
	// A subagent's job log is streamed prose: whole paragraphs land as single
	// very long lines. The output view must SOFT-WRAP them so the full content
	// stays readable, instead of truncating each line to the modal width.
	const n = 200
	long := strings.Repeat("z", n) // one long line, no spaces → forces a hard wrap
	m, _ := bgModel([]shell3.JobInfo{{ID: "bg_aaa", Cmd: "a", PID: 1}},
		map[string]string{"bg_aaa": long})
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // open the output view
	plain := stripANSI(m.View().Content)
	if got := strings.Count(plain, "z"); got != n {
		t.Fatalf("output view should wrap the long line and show all %d chars, got %d:\n%s", n, got, plain)
	}
}

func TestBackground_RendersSubagentTranscript(t *testing.T) {
	// Transcript is now messages.jsonl format (llm.Message records per line).
	raw := strings.Join([]string{
		`{"role":"assistant","reasoning_content":"pondering the question","tool_calls":[{"ID":"c1","Name":"bash","RawArgs":"{\"command\":\"ls\"}"}]}`,
		`{"role":"tool","content":"file1.go\nfile2.go","name":"bash","tool_call_id":"c1"}`,
		`{"role":"assistant","content":"the answer is **42**"}`,
	}, "\n")
	fc := &fakeCmds{
		jobs:          []shell3.JobInfo{{ID: "bg_sub", Cmd: "describe-42", Kind: shell3.JobSubagent, PID: 1}},
		jobTranscript: map[string]string{"bg_sub": raw},
	}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // open the subagent's output
	if !m.bg.isTranscript {
		t.Fatal("a job with a transcript should render structured, not stdout")
	}
	plain := stripANSI(m.View().Content)
	for _, want := range []string{"thinking", "pondering the question", "bash", "42", "file1.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("transcript modal missing %q in:\n%s", want, plain)
		}
	}
}

func TestBackground_TranscriptHardWrapsLongTokens(t *testing.T) {
	// A subagent answer with a long unbreakable token (a URL, path, or hash) would,
	// via glamour, produce a line wider than the modal — bgPanel.Width would then
	// re-wrap it into extra terminal rows, desyncing the one-row-per-element scroll
	// and height math. Every row must be hard-wrapped to the content width.
	longTok := strings.Repeat("x", 200)
	raw := `{"role":"assistant","content":"see ` + longTok + ` end"}`
	fc := &fakeCmds{
		jobs:          []shell3.JobInfo{{ID: "bg_sub", Cmd: "task", Kind: shell3.JobSubagent, PID: 1}},
		jobTranscript: map[string]string{"bg_sub": raw},
	}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20}) // narrow terminal
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	w := m.bgOutputWidth()
	for i, row := range m.bgWrappedLines() {
		if ansi.StringWidth(row) > w {
			t.Fatalf("transcript row %d width %d exceeds content width %d: %q", i, ansi.StringWidth(row), w, row)
		}
	}
}

func TestBackground_OpensAtBottomAndG(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	m, _ := bgModel([]shell3.JobInfo{{ID: "bg_aaa", Cmd: "a", PID: 1}},
		map[string]string{"bg_aaa": b.String()})
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // opens at the bottom (most recent)
	if m.bg.scroll == 0 {
		t.Fatal("output view should open scrolled to the bottom for long output")
	}
	bottom := m.bg.scroll
	m.Update(keyRune('g')) // jump to top
	if m.bg.scroll != 0 {
		t.Fatalf("g should jump to top, got scroll=%d", m.bg.scroll)
	}
	m.Update(keyRune('G')) // jump back to bottom
	if m.bg.scroll != bottom {
		t.Fatalf("G should jump to bottom (%d), got %d", bottom, m.bg.scroll)
	}
}

// longOutputModel opens a job whose stdout is many short lines, in the output view.
func longOutputModel(t *testing.T) *model {
	t.Helper()
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	m, _ := bgModel([]shell3.JobInfo{{ID: "bg_aaa", Cmd: "a", PID: 1}},
		map[string]string{"bg_aaa": b.String()})
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // open the output view (at bottom)
	return m
}

func TestBackground_DoesNotOverScroll(t *testing.T) {
	m := longOutputModel(t)
	// Hammer j far past the end — it must clamp at the last full screenful, never
	// collapse the view toward a single trailing line.
	for i := 0; i < 500; i++ {
		m.Update(keyRune('j'))
	}
	want := len(m.bgWrappedLines()) - m.bgModalHeight()
	if m.bg.scroll != want {
		t.Fatalf("scroll should clamp to the last screenful: scroll=%d want=%d", m.bg.scroll, want)
	}
	// The view still shows a full page of content, not one line.
	if n := strings.Count(stripANSI(m.View().Content), "line "); n < m.bgModalHeight()-1 {
		t.Fatalf("over-scroll collapsed the view to %d content lines:\n%s", n, stripANSI(m.View().Content))
	}
}

func TestBackground_MouseWheelScrolls(t *testing.T) {
	wheel := func(b tea.MouseButton) tea.MouseWheelMsg { return tea.MouseWheelMsg{Button: b} }
	m := longOutputModel(t)
	atBottom := m.bg.scroll
	if atBottom == 0 {
		t.Fatal("precondition: long output should open scrolled down")
	}
	m.Update(wheel(tea.MouseWheelUp)) // scroll up toward the top
	if m.bg.scroll >= atBottom {
		t.Fatalf("wheel up should scroll up: %d (was %d)", m.bg.scroll, atBottom)
	}
	up := m.bg.scroll
	m.Update(wheel(tea.MouseWheelDown)) // scroll back down
	if m.bg.scroll <= up {
		t.Fatalf("wheel down should scroll down: %d (was %d)", m.bg.scroll, up)
	}
	// Wheel down also clamps — no over-scroll past the last screenful.
	for i := 0; i < 200; i++ {
		m.Update(wheel(tea.MouseWheelDown))
	}
	if want := len(m.bgWrappedLines()) - m.bgModalHeight(); m.bg.scroll != want {
		t.Fatalf("wheel down should clamp to %d, got %d", want, m.bg.scroll)
	}
}

func TestBackground_EmptyList(t *testing.T) {
	m, _ := bgModel(nil, nil)
	m.openBackground()
	if !strings.Contains(stripANSI(m.View().Content), "no background jobs") {
		t.Fatalf("empty modal should say so:\n%s", stripANSI(m.View().Content))
	}
	// ctrl+x with nothing selected is a no-op (no panic, no kill).
	m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
}

// TestBackground_DoneJobShowsStatusLabel verifies that a finished job renders
// "✓ done" (zero exit) or "✗ error" (non-zero exit) in the modal list.
func TestBackground_DoneJobShowsStatusLabel(t *testing.T) {
	exitZero, exitOne := 0, 1
	jobs := []shell3.JobInfo{
		{ID: "bg_ok", Cmd: "ok-cmd", PID: 1, Done: true, Exit: &exitZero},
		{ID: "bg_err", Cmd: "err-cmd", PID: 2, Done: true, Exit: &exitOne},
		{ID: "bg_run", Cmd: "run-cmd", PID: 3, Done: false},
	}
	m, _ := bgModel(jobs, nil)
	m.openBackground()
	plain := stripANSI(m.View().Content)
	if !strings.Contains(plain, "✓ done") {
		t.Fatalf("done job with exit 0 should show '✓ done':\n%s", plain)
	}
	if !strings.Contains(plain, "✗ error") {
		t.Fatalf("done job with non-zero exit should show '✗ error':\n%s", plain)
	}
	// Running job should not show either label.
	if strings.Count(plain, "✓ done") != 1 || strings.Count(plain, "✗ error") != 1 {
		t.Fatalf("unexpected label counts in:\n%s", plain)
	}
}

// TestBackground_DoneSubagentTranscriptViewable verifies that selecting a done
// subagent job and pressing enter opens its transcript.
func TestBackground_DoneSubagentTranscriptViewable(t *testing.T) {
	raw := strings.Join([]string{
		`{"role":"assistant","content":"done answer"}`,
	}, "\n")
	fc := &fakeCmds{
		jobs: []shell3.JobInfo{
			{ID: "sub1", Cmd: "task", Kind: shell3.JobSubagent, PID: 0, Done: true},
		},
		jobTranscript: map[string]string{"sub1": raw},
	}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openBackground()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.bg.viewID != "sub1" {
		t.Fatalf("enter should open the done subagent job, got %q", m.bg.viewID)
	}
	plain := stripANSI(m.View().Content)
	if !strings.Contains(plain, "done answer") {
		t.Fatalf("done subagent transcript not shown:\n%s", plain)
	}
}

// TestBackground_TickRefreshesJobListWhenOpen verifies that a bgPollTickMsg
// while the modal is open refreshes m.bg.jobs to reflect newly-finished jobs.
func TestBackground_TickRefreshesJobListWhenOpen(t *testing.T) {
	runningJob := shell3.JobInfo{ID: "bg1", Cmd: "run", PID: 1, Done: false}
	fc := &fakeCmds{jobs: []shell3.JobInfo{runningJob}}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openBackground()

	// Initially the job is running.
	if len(m.bg.jobs) != 1 || m.bg.jobs[0].Done {
		t.Fatalf("expected one running job, got %+v", m.bg.jobs)
	}

	// Simulate the job finishing: update fc.jobs to report Done=true.
	exitCode := 0
	fc.jobs = []shell3.JobInfo{
		{ID: "bg1", Cmd: "run", PID: 1, Done: true, Exit: &exitCode},
	}

	// Fire a bgPollTickMsg — the handler should refresh m.bg.jobs.
	m.Update(bgPollTickMsg{})

	if len(m.bg.jobs) != 1 || !m.bg.jobs[0].Done {
		t.Fatalf("after tick, bgJobs should show Done=true, got %+v", m.bg.jobs)
	}
}
