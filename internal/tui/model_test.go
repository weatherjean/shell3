package tui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/shell3"
)

func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func closedSend(record func(string)) func(string) (<-chan shell3.Event, context.CancelFunc) {
	return func(p string) (<-chan shell3.Event, context.CancelFunc) {
		if record != nil {
			record(p)
		}
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
}

func sized(send func(string) (<-chan shell3.Event, context.CancelFunc)) *model {
	return sizedWith(send, nil)
}

// sizedWith is sized with an injected sessionCmds fake, for tests that need
// Jobs()/Snapshot()/etc. wired.
func sizedWith(send func(string) (<-chan shell3.Event, context.CancelFunc), cmds sessionCmds) *model {
	m := newModel(send, cmds, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// frame is the standard render→snapshot→inspect helper: build a model with
// sized/sizedWith, then drive it through frame with whatever keys/messages the
// test cares about, and assert on the returned ANSI-free plain-text screen (or
// on m.uiSnapshot() for structured state) — no PTY required. It replaces
// ad-hoc `stripANSI(m.View().Content)` call sites one at a time; both forms
// coexist meanwhile.
func frame(m *model, msgs ...tea.Msg) string {
	for _, msg := range msgs {
		m.Update(msg)
	}
	return stripANSI(m.View().Content)
}

func TestSubmitSendsPromptAndAddsUserItem(t *testing.T) {
	var prompt string
	sent := false
	m := sized(closedSend(func(p string) { prompt, sent = p, true }))
	m.ta.SetValue("hello world")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !sent || prompt != "hello world" {
		t.Fatalf("submit should send the prompt: sent=%v prompt=%q", sent, prompt)
	}
	last := m.tr.items[m.tr.count()-1]
	if m.tr.count() < 1 || last.Kind != ItemUser || last.Text != "hello world" {
		t.Fatalf("submit should add a user item: %+v", m.tr.items)
	}
	if !m.busy {
		t.Fatal("should be busy after submit")
	}
	if m.ta.Value() != "" {
		t.Fatalf("input should reset after submit, got %q", m.ta.Value())
	}
}

// There is no vim NORMAL mode anymore: esc on the main input is a plain
// keystroke (it only dismisses a mouse selection, handled in handleKey), and
// the draft is untouched either way.
func TestEscDoesNotClearDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("draft text")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ta.Value() != "draft text" {
		t.Fatalf("esc must NOT clear the draft, got %q", m.ta.Value())
	}
}

// No normal mode: with an empty input, "j" (and any other plain letter) types
// into the textarea rather than moving a line cursor or doing anything special.
func TestPlainKeyTypesIntoEmptyInput(t *testing.T) {
	m := sized(closedSend(nil))
	m.Update(keyRune('j'))
	if m.ta.Value() != "j" {
		t.Fatalf("j with empty input should type a literal j, got %q", m.ta.Value())
	}
}

func TestEmptySubmitIsNoop(t *testing.T) {
	sent := false
	m := sized(closedSend(func(string) { sent = true }))
	m.ta.SetValue("   ")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent {
		t.Fatal("blank submit should not send")
	}
}

// Folding is now mouse-only (click) or via the "fold"/"unfold" palette
// commands — there is no NORMAL-mode Enter-to-fold.
func TestClickTogglesFoldOnTheOnlyBlock(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	m.tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "out"})
	if !m.tr.items[0].Folded {
		t.Fatal("tool block should start folded")
	}
	m.refresh(false)
	y := m.blockStarts[0] - m.vp.YOffset()
	m.handleClick(y)
	if m.tr.items[0].Folded {
		t.Fatal("clicking a foldable block should unfold it")
	}
}

func TestInputGrowsWithNewlinesAndShrinksViewport(t *testing.T) {
	m := sized(closedSend(nil))
	base := m.vp.Height()
	// Exceed the 3-row minimum so the input genuinely grows.
	m.ta.SetValue("l1\nl2\nl3\nl4\nl5\nl6")
	m.relayout()
	if m.ta.Height() < 6 {
		t.Fatalf("textarea should auto-grow to >=6 rows, got %d", m.ta.Height())
	}
	if m.vp.Height() >= base {
		t.Fatalf("viewport should shrink as input grows: now %d, was %d", m.vp.Height(), base)
	}
	// Total rows still fit the terminal: vp + input + footer + 1 spacer line (one
	// above the input) == height.
	if m.vp.Height()+m.ta.Height()+1+1 != m.height {
		t.Fatalf("rows must sum to height: %d+%d+1+1 != %d", m.vp.Height(), m.ta.Height(), m.height)
	}
}

func TestHelpOverlayOpensAndCloses(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40}) // tall enough for the full overlay
	m.Update(keyRune('?'))                             // empty input → '?' opens help
	if !m.helpOpen {
		t.Fatal("? should open the help overlay")
	}
	plain := stripANSI(m.View().Content)
	for _, want := range []string{"shell3 — keys", "ctrl+p", "clear"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help overlay missing %q", want)
		}
	}
	m.Update(keyRune('j')) // any key closes
	if m.helpOpen {
		t.Fatal("any key should dismiss the help overlay")
	}
}

// fakeCmds is a minimal sessionCmds for agent-cycle tests.
type fakeCmds struct {
	names         []string
	active        string
	status        string
	queued        bool
	compactQueued bool
	jobs          []shell3.JobInfo
	jobOut        map[string]string
	jobTranscript map[string]string
	killed        []string
	killErr       error // when set, KillJob returns it (exercises the failure notice)
	safetyOff     bool
}

func (f *fakeCmds) Clear() error                 { return nil }
func (f *fakeCmds) Rollback() (bool, error)      { return false, nil }
func (f *fakeCmds) Prune(string) (string, error) { return "", nil }
func (f *fakeCmds) QueueCompact()                { f.compactQueued = true }
func (f *fakeCmds) AgentNames() []string         { return f.names }
func (f *fakeCmds) ActiveAgent() string          { return f.active }
func (f *fakeCmds) Snapshot() shell3.Snapshot {
	// Mirror the real Snapshot: Model is the parsed middle of the status line.
	_, model := chat.SplitStatus(f.status)
	return shell3.Snapshot{Agent: f.active, StatusLine: f.status, Model: model}
}
func (f *fakeCmds) HasQueuedInput() bool           { return f.queued }
func (f *fakeCmds) Jobs() []shell3.JobInfo         { return f.jobs }
func (f *fakeCmds) JobOutput(id string) string     { return f.jobOut[id] }
func (f *fakeCmds) JobTranscript(id string) string { return f.jobTranscript[id] }
func (f *fakeCmds) KillJob(id string) error        { f.killed = append(f.killed, id); return f.killErr }
func (f *fakeCmds) SetSafetyOff(off bool)          { f.safetyOff = off }
func (f *fakeCmds) SwitchAgent(name string) error {
	f.active = name
	return nil
}

func TestTabCyclesAgent(t *testing.T) {
	fc := &fakeCmds{names: []string{"main", "research", "build"}, active: "main"}
	m := sizedWith(closedSend(nil), fc)

	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "research" || m.agentName != "research" {
		t.Fatalf("tab should cycle to research, got active=%q name=%q", fc.active, m.agentName)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "main" {
		t.Fatalf("tab should wrap back to main, got %q", fc.active)
	}
}

func TestTabIsNoopWhileBusy(t *testing.T) {
	fc := &fakeCmds{names: []string{"main", "research"}, active: "main"}
	m := sizedWith(closedSend(nil), fc)
	m.busy = true
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "main" {
		t.Fatalf("tab must not switch agents while busy, got %q", fc.active)
	}
}

func TestFooterShowsActiveAgent(t *testing.T) {
	m := sized(closedSend(nil))
	m.agentName = "research"
	if !strings.Contains(stripANSI(m.renderFooter()), "research") {
		t.Fatalf("footer should show active agent, got %q", stripANSI(m.renderFooter()))
	}
}

func TestInsertQuestionMarkOpensHelpWhenEmpty(t *testing.T) {
	m := sized(closedSend(nil))
	// Empty input: '?' opens help instead of typing.
	m.Update(keyRune('?'))
	if !m.helpOpen {
		t.Fatal("? on empty input should open help in INSERT")
	}
	if m.ta.Value() != "" {
		t.Fatalf("? should not be typed when opening help, got %q", m.ta.Value())
	}
}

func TestInsertQuestionMarkTypesWhenNotEmpty(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("how")
	m.Update(keyRune('?'))
	if m.helpOpen {
		t.Fatal("? with text present should NOT open help")
	}
	if m.ta.Value() != "how?" {
		t.Fatalf("? should type normally with text present, got %q", m.ta.Value())
	}
}

func TestWelcomeCardShowsThenHides(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(stripANSI(m.View().Content), "shell3") {
		t.Fatal("welcome card should render before the first message")
	}
	if !strings.Contains(stripANSI(m.View().Content), "build") {
		t.Fatal("welcome card should show the active agent")
	}
	// After a message the card is gone (transcript drives the viewport).
	m.tr.AddUser("hi")
	m.refresh(true)
	if strings.Contains(stripANSI(m.vp.View()), "Unix-composable") {
		t.Fatal("welcome card should disappear once the transcript is non-empty")
	}
}

func TestCtrlUClearsInput(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("draft text")
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.ta.Value() != "" {
		t.Fatalf("ctrl+u should clear the input, got %q", m.ta.Value())
	}
}

func TestEditFileOutputColorizedAsDiff(t *testing.T) {
	out := colorizeDiff("@@ -0,0 +1,2 @@\n+added line\n-removed line\n context")
	plain := stripANSI(out)
	for _, want := range []string{"@@ -0,0 +1,2 @@", "+added line", "-removed line", "context"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("diff text must survive colorization, missing %q", want)
		}
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("diff output should contain ANSI color codes")
	}
}

func TestFooterShowsModelAndAgentOnce(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "openai │ gpt-x │ high")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	foot := stripANSI(m.renderFooter())
	if strings.Count(foot, "build") != 1 {
		t.Fatalf("agent should appear exactly once (the pill): %q", foot)
	}
	if !strings.Contains(foot, "gpt-x") {
		t.Fatalf("footer should show the model: %q", foot)
	}
	if strings.Contains(foot, "openai") || strings.Contains(foot, "high") {
		t.Fatalf("footer should not show the provider/effort triplet: %q", foot)
	}
}

func TestCommandPaletteFilters(t *testing.T) {
	m := sized(closedSend(nil))
	m.openPalette()
	m.palette.query = "ag"
	box := stripANSI(m.paletteBox())
	if !strings.Contains(box, "agent") || !strings.Contains(box, "agents") {
		t.Fatalf("palette should list agent commands for 'ag':\n%s", box)
	}
	if strings.Contains(box, "clear") {
		t.Fatalf("palette should filter out non-matching commands:\n%s", box)
	}
}

// A bracketed paste (tea.PasteMsg, not a KeyPressMsg) must still recompute the
// layout, so a multi-line paste doesn't leave the footer/viewport stale until
// the next keystroke. A taller input shrinks the viewport.
func TestPasteRecomputesLayout(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	vpBefore := m.vp.Height()
	m.Update(tea.PasteMsg{Content: "a\nb\nc\nd\ne\nf"})
	if m.vp.Height() >= vpBefore {
		t.Fatalf("multi-line paste should shrink the viewport via relayout (before %d, after %d)", vpBefore, m.vp.Height())
	}
}

// The input shows a scroll indicator only when it has grown past its visible
// height (so off-screen text isn't silently hidden).
func TestInputScrollIndicator(t *testing.T) {
	// A fitting input (empty, a single char, or a single short line) must show
	// NO indicator — render the input first, as a real frame does.
	for _, val := range []string{"", "a", "hello world"} {
		m := newModel(closedSend(nil), nil, "", "")
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
		m.ta.SetValue(val)
		m.relayout()
		m.ta.View()
		if got := stripANSI(m.inputScrollIndicator()); got != "" {
			t.Fatalf("input %q fits but showed an indicator %q", val, got)
		}
	}
	// An input taller than its visible height shows a scroll arrow.
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m.ta.SetValue(strings.Repeat("line\n", 40))
	m.relayout()
	m.ta.View()
	if ind := stripANSI(m.inputScrollIndicator()); !strings.ContainsAny(ind, "▲▼") {
		t.Fatalf("an overflowing input should show a scroll arrow, got %q", ind)
	}
}

// The "›" prompt marker shows only on a single-line input; a multi-line input
// drops it so continuation rows aren't cluttered.
func TestPromptMarkerOnlyWhenSingleLine(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.ta.SetValue("one line")
	if !strings.Contains(stripANSI(m.ta.View()), "›") {
		t.Fatalf("single-line input should show the › prompt:\n%s", stripANSI(m.ta.View()))
	}
	m.ta.SetValue("first\nsecond")
	if strings.Contains(stripANSI(m.ta.View()), "›") {
		t.Fatalf("multi-line input should NOT show the › prompt:\n%s", stripANSI(m.ta.View()))
	}
}

// compact is a real, handled command; it must be discoverable in the palette.
func TestCommandPalette_ListsCompact(t *testing.T) {
	m := sized(closedSend(nil))
	m.openPalette()
	m.palette.query = "comp"
	if box := stripANSI(m.paletteBox()); !strings.Contains(box, "compact") {
		t.Fatalf("palette should list compact for 'comp':\n%s", box)
	}
}

// The help command opens the help overlay (same as '?') rather than dumping a
// one-line text — one help surface, no dual handling.
func TestHelpCommand_OpensOverlay(t *testing.T) {
	m := sized(closedSend(nil))
	m.runCommand("help")
	if !m.helpOpen {
		t.Fatal(":help should open the help overlay")
	}
}

// The help overlay's command reference is derived from exCommands (single source
// of truth), so every handled command — including the ones that used to be
// missing from one list or another — appears, and the lists can't drift.
func TestHelpOverlay_ListsEveryPaletteCommand(t *testing.T) {
	m := sized(closedSend(nil))
	box := stripANSI(m.helpBox())
	for _, c := range exCommands {
		if !strings.Contains(box, c.name) {
			t.Errorf("help overlay is missing %s (command reference must list every exCommands entry):\n%s", c.name, box)
		}
	}
	// Spot-check the ones that were previously missing from one list or another.
	for _, want := range []string{"compact", "disable_safety", "background"} {
		if !strings.Contains(box, want) {
			t.Errorf("help overlay missing %s", want)
		}
	}
}

// Scrolling up (mouse wheel — there is no NORMAL-mode gg/G anymore) breaks the
// autoscroll lock; the "follow" palette command re-locks it and jumps back to
// the bottom.
func TestFollowBreaksOnScrollUpAndRelocksViaCommand(t *testing.T) {
	m := sized(closedSend(nil))
	for i := 0; i < 60; i++ {
		m.tr.AddUser(fmt.Sprintf("line %d", i))
	}
	m.follow = true
	m.refresh(true)
	if !m.vp.AtBottom() {
		t.Fatal("should start at the bottom")
	}
	m.handleWheel(tea.Mouse{Button: tea.MouseWheelUp})
	if m.follow {
		t.Fatal("scrolling up should break the autoscroll lock")
	}
	m.runCommand("follow")
	if !m.follow || !m.vp.AtBottom() {
		t.Fatal("the follow command should re-lock autoscroll and jump to the bottom")
	}
}

func TestMidTurnSubmitSteersInsteadOfRefusing(t *testing.T) {
	var steered []string
	m := sized(closedSend(nil))
	m.steer = func(s string) { steered = append(steered, s) }
	m.busy = true
	m.ta.SetValue("also handle the edge case")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(steered) != 1 || steered[0] != "also handle the edge case" {
		t.Fatalf("submit while busy should steer the turn: %v", steered)
	}
	if !m.busy {
		t.Fatal("steering must not end the running turn")
	}
	if m.ta.Value() != "" {
		t.Fatalf("input should clear after steering, got %q", m.ta.Value())
	}
	last := m.tr.items[m.tr.count()-1]
	if !last.Steer || last.Text != "also handle the edge case" {
		t.Fatalf("a steer item should be shown: %+v", last)
	}
}

func TestQueuedSteeringAutoRunsAtTurnEnd(t *testing.T) {
	fc := &fakeCmds{queued: true}
	ran := false
	m := newModel(closedSend(nil), fc, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran = true
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
	m.busy = true
	m.Update(eventMsg{ok: false}) // turn ends with steering still queued
	if !ran {
		t.Fatal("queued steering should auto-run a follow-up turn at turn end")
	}
	if !m.busy {
		t.Fatal("the follow-up turn should mark the model busy again")
	}
}

func TestCancelDuringThinkingShowsMarkerAndFolds(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	m.cancel = func() {}
	// Mid-thinking: an open, unfolded reasoning block, no tokens yet.
	m.tr.Apply(shell3.Event{Kind: shell3.Reasoning, Text: "considering the request"})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) // ctrl+c → cancel
	if !m.canceling {
		t.Fatal("ctrl+c while busy should set canceling")
	}
	// Turn ends by channel close WITHOUT an Error event (the thinking-cancel case).
	m.Update(eventMsg{ok: false})
	plain := stripANSI(mustRender(m.tr))
	if !strings.Contains(plain, "⊘ canceled") {
		t.Fatalf("canceled marker should render even when only thinking was on screen:\n%s", plain)
	}
	if !m.tr.items[0].Folded {
		t.Fatal("the half-streamed thinking block should fold on cancel")
	}
	if m.canceling || m.busy {
		t.Fatal("canceling/busy should be cleared after the turn ends")
	}
}

func TestCanceledErrorEventSuppressed(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	// An Error(context.Canceled) must NOT become a red "✗ context canceled".
	m.Update(eventMsg{ok: true, ev: shell3.Event{Kind: shell3.Error, Err: context.Canceled}, ch: nil})
	plain := stripANSI(mustRender(m.tr))
	if strings.Contains(plain, "context canceled") || strings.Contains(plain, "✗") {
		t.Fatalf("raw context-canceled error must be suppressed:\n%s", plain)
	}
}

func TestFooterThinkingHasRainbowBackground(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	foot := m.renderFooter()
	if !strings.Contains(stripANSI(foot), "thinking") {
		t.Fatal("busy footer should say thinking")
	}
	if !strings.Contains(foot, "\x1b[48;2;") {
		t.Fatal("thinking indicator should have a truecolor (rainbow) background")
	}
}

func TestFooterQuitArmedShowsRedBar(t *testing.T) {
	m := sized(closedSend(nil))
	m.quitArmed = true
	foot := m.renderFooter()
	if !strings.Contains(stripANSI(foot), "press ctrl+c again to quit") {
		t.Fatal("quit-armed footer should prompt to press again")
	}
	if !strings.Contains(foot, bgSeq(cRed)) { // the quit bar carries cRed as its background
		t.Fatalf("quit-armed bar should have a red (cRed) background:\n%q", foot)
	}
}

func TestLightTerminalSwitchesToLightPalette(t *testing.T) {
	setPalette(t, darkPalette)
	m := sized(closedSend(nil))
	if !m.isDark {
		t.Fatal("model should default to the dark palette")
	}
	white, _ := colorful.Hex("#FFFFFF")
	m.Update(tea.BackgroundColorMsg{Color: white})
	if m.isDark {
		t.Fatal("a white terminal background should switch the model to light")
	}
	if cFg != lightPalette.fg {
		t.Fatalf("active fg should be the light palette's, got %v want %v", cFg, lightPalette.fg)
	}
}

// TestSameModeBackgroundReportIsNoOp guards applyTerminalBackground's early
// return: a background report matching the current mode must not flip the palette
// (which would rebuild every style and re-render the whole transcript for nothing).
func TestSameModeBackgroundReportIsNoOp(t *testing.T) {
	setPalette(t, darkPalette) // known dark baseline, matching m.isDark
	m := sized(closedSend(nil))
	if !m.isDark {
		t.Fatal("model should default to dark")
	}
	black, _ := colorful.Hex("#000000")
	m.Update(tea.BackgroundColorMsg{Color: black}) // a dark report on an already-dark model
	if !m.isDark {
		t.Fatal("a dark report on a dark model must leave it dark")
	}
	if cFg != darkPalette.fg {
		t.Fatalf("same-mode report must not rebuild the palette, got fg %v", cFg)
	}
}

func TestThemeOverrideSurvivesPaletteSwitch(t *testing.T) {
	setPalette(t, darkPalette)
	m := sized(closedSend(nil))
	magenta := lipgloss.Color("#FF00FF")
	m.themeOverride = map[string]color.Color{"primary": magenta}
	m.applyTheme() // dark base + override
	if cPrimary != magenta {
		t.Fatalf("override not applied on the dark base: got %v", cPrimary)
	}
	// Sensing a light terminal rebuilds from the light base but must keep the
	// override on top.
	white, _ := colorful.Hex("#FFFFFF")
	m.Update(tea.BackgroundColorMsg{Color: white})
	if cFg != lightPalette.fg {
		t.Fatalf("should switch to the light fg, got %v", cFg)
	}
	if cPrimary != magenta {
		t.Fatalf("the theme override should survive the switch to light, got %v", cPrimary)
	}
}

func TestCustomWelcomeReplacesBuiltIn(t *testing.T) {
	m := sized(closedSend(nil))
	// Default: the built-in card (carries the shell3 brand).
	if !strings.Contains(stripANSI(m.welcomeCard()), "shell3") {
		t.Fatal("built-in welcome card should mention shell3")
	}
	// A custom card replaces it verbatim, including any embedded ANSI.
	m.welcome = "\x1b[31m✦ MY SPLASH ✦\x1b[0m"
	got := m.welcomeCard()
	if got != m.welcome {
		t.Fatalf("custom welcome should render verbatim, got %q", got)
	}
	if strings.Contains(stripANSI(got), "shell3") {
		t.Fatal("custom card must not include the built-in card's content")
	}
}

func TestViewUsesPassthroughBackground(t *testing.T) {
	m := sized(closedSend(nil))
	v := m.View()
	// Backgrounds pass through to the terminal — shell3 never paints a canvas
	// (adaptive foreground colors keep text legible on light and dark instead).
	if v.BackgroundColor != nil {
		t.Errorf("View must not force a terminal background, got %v", v.BackgroundColor)
	}
}

func TestEditorResultLoadsIntoDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.Update(openEditorMsg{text: "a big\nmulti-line prompt\n"})
	if m.ta.Value() != "a big\nmulti-line prompt" {
		t.Fatalf("editor result should load into the draft, got %q", m.ta.Value())
	}
}

func TestEditorResultErrorShowsNotice(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("keep me")
	m.Update(openEditorMsg{err: fmt.Errorf("boom")})
	if m.ta.Value() != "keep me" {
		t.Fatal("a failed editor run must not clobber the draft")
	}
	if !strings.Contains(m.notice, "boom") {
		t.Fatalf("editor error should surface in the notice, got %q", m.notice)
	}
}

func TestResolveEditorPrefersEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor --wait")
	if got := resolveEditor(); got != "myeditor --wait" {
		t.Fatalf("resolveEditor should return $EDITOR verbatim (args preserved), got %q", got)
	}
	t.Setenv("VISUAL", "vis")
	if got := resolveEditor(); got != "vis" {
		t.Fatalf("resolveEditor should prefer $VISUAL, got %q", got)
	}
}

func TestCommandGateConfirmDefaultsYesOnEnter(t *testing.T) {
	m := sized(closedSend(nil))
	reply := make(chan bool, 1)
	m.Update(confirmMsg{req: &confirmReq{command: "rm -rf /tmp/x", reply: reply}})
	if m.confirm == nil || !m.confirmYes {
		t.Fatal("confirm modal should open with Yes selected")
	}
	// Modal renders over the transcript with the command + buttons.
	plain := stripANSI(m.View().Content)
	for _, want := range []string{"command gate", "rm -rf /tmp/x", "Yes", "No"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("confirm modal missing %q", want)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // Enter on default Yes → allow
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("Enter on default Yes should allow")
		}
	default:
		t.Fatal("Enter should have replied to the asker")
	}
	if m.confirm != nil {
		t.Fatal("modal should dismiss after answering")
	}
}

func TestCommandGateConfirmDenyKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'n'},
		{Code: tea.KeyEscape},
	} {
		m := sized(closedSend(nil))
		reply := make(chan bool, 1)
		m.Update(confirmMsg{req: &confirmReq{command: "curl evil", reply: reply}})
		m.Update(key)
		select {
		case ok := <-reply:
			if ok {
				t.Fatalf("key %v should deny", key)
			}
		default:
			t.Fatalf("key %v should reply deny", key)
		}
	}
}

func TestDisableSafetyTogglesSessionAndShowsBang(t *testing.T) {
	// The auto-allow itself lives in the Session's Asker wrapper
	// (shell3.Session.SetSafetyOff); the TUI's job is to propagate the toggle
	// and show the "!" indicator.
	fc := &fakeCmds{}
	m := newModel(closedSend(nil), fc, "build", "openai │ gpt-x")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.runCommand("disable_safety")
	if !m.safetyOff {
		t.Fatal(":disable_safety should turn safety off")
	}
	if !fc.safetyOff {
		t.Fatal(":disable_safety should propagate the toggle to the session")
	}
	if !strings.Contains(stripANSI(m.renderFooter()), "!") {
		t.Fatal("footer should show the ! indicator when safety is off")
	}
	// Toggle back on.
	m.runCommand("disable_safety")
	if m.safetyOff || fc.safetyOff {
		t.Fatal(":disable_safety should toggle back on (model + session)")
	}
}

// Tab in the palette completes the currently-selected row's name into the
// input (not a common-prefix guess across all matches).
func TestPaletteTabCompletesSelection(t *testing.T) {
	m := sized(closedSend(nil))
	m.openPalette()
	// "dis" → only disable_safety matches → tab completes it.
	m.palette.query = "dis"
	m.handlePaletteKey(tea.KeyPressMsg{Code: tea.KeyTab}, "tab")
	if m.palette.query != "disable_safety" {
		t.Fatalf("tab should complete the sole match, got %q", m.palette.query)
	}
	// "agent" → matches agent/agents; tab completes the selected (first) row.
	m.palette.query = "agent"
	m.palette.sel = 0
	m.handlePaletteKey(tea.KeyPressMsg{Code: tea.KeyTab}, "tab")
	if m.palette.query != "agent" {
		t.Fatalf("tab should complete the selected row's name, got %q", m.palette.query)
	}
}

func TestStartSpinNoDuplicateChain(t *testing.T) {
	m := sized(closedSend(nil))
	if m.startSpin() == nil {
		t.Fatal("first startSpin should start a tick chain")
	}
	if m.startSpin() != nil {
		t.Fatal("a second startSpin while already spinning must be a no-op (no duplicate chain)")
	}
	// A tick that fires while not busy ends the chain.
	m.busy = false
	m.Update(spinnerTickMsg{})
	if m.spinning {
		t.Fatal("a tick with !busy should clear spinning")
	}
	if m.startSpin() == nil {
		t.Fatal("after the chain ends, startSpin should be able to start again")
	}
}

func TestApplyAgentRefreshesStatusAndContext(t *testing.T) {
	fc := &fakeCmds{active: "b", status: "openai │ gpt-b │ low"}
	m := newModel(closedSend(nil), fc, "a", "openai │ gpt-a │ high")
	m.applyAgent()
	if m.agentName != "b" {
		t.Fatalf("applyAgent should refresh the agent from the snapshot: %q", m.agentName)
	}
	if m.modelName != "gpt-b" {
		t.Fatalf("footer model label should track the new agent, got %q", m.modelName)
	}
}

func TestHandleWakeDrainsQueuedWhenIdle(t *testing.T) {
	fc := &fakeCmds{queued: true}
	ran := false
	m := newModel(closedSend(nil), fc, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.sessionName = "main"
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran = true
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
	// Wake for this session while idle → drain the queued inbox.
	m.handleWake(shell3.HostEvent{Session: "main", Kind: shell3.Wake})
	if !ran || !m.busy {
		t.Fatal("an idle wake for this session with queued input should run a follow-up turn")
	}
	// Wake while busy → no-op.
	m.busy = true
	ran = false
	m.handleWake(shell3.HostEvent{Session: "main", Kind: shell3.Wake})
	if ran {
		t.Fatal("a wake while busy must be ignored (the turn drains its own inbox)")
	}
	// Wake for a different session → no-op.
	m.busy = false
	m.handleWake(shell3.HostEvent{Session: "other", Kind: shell3.Wake})
	if ran {
		t.Fatal("a wake naming a different session must be ignored")
	}
}

func TestEditorEmptySaveKeepsDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("keep me")
	m.Update(openEditorMsg{text: "   \n\n"})
	if m.ta.Value() != "keep me" {
		t.Fatalf("an empty editor save must not wipe the draft, got %q", m.ta.Value())
	}
}

func TestFooterShowsContextWindowFill(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "openai │ gpt-x")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.tokens, m.contextWindow = 5000, 10000
	foot := stripANSI(m.renderFooter())
	// The fill sits right after the model name.
	if !strings.Contains(foot, "gpt-x  (ctx: 50%)") {
		t.Fatalf("footer should show the context-window fill after the model: %q", foot)
	}
}

func TestCompactedEventDropsTokenMeter(t *testing.T) {
	m := sized(closedSend(nil))
	m.tokens, m.promptTokens, m.completTokens = 90000, 85000, 5000
	// A compacted event carries the post-compaction estimate; the meter should
	// drop to it immediately (completion cleared — no response yet).
	m.Update(eventMsg{ok: true, ch: nil, ev: shell3.Event{
		Kind: shell3.Compacted, Text: "context auto-compacted at 90000 tokens",
		PromptTokens: 1200, TotalTokens: 1200,
	}})
	if m.tokens != 1200 || m.promptTokens != 1200 || m.completTokens != 0 {
		t.Fatalf("compaction should drop the meter to the estimate: tokens=%d prompt=%d compl=%d",
			m.tokens, m.promptTokens, m.completTokens)
	}
}

func TestCtrlCRequiresTwoPresses(t *testing.T) {
	m := sized(closedSend(nil))
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.quitArmed {
		t.Fatal("first ctrl+c should arm quit, not exit")
	}
	// A different key disarms.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.quitArmed {
		t.Fatal("a non-ctrl+c key should disarm quit")
	}
}
