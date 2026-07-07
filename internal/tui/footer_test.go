package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestFooterShowsBgCountAndNotice(t *testing.T) {
	fc := &fakeCmds{jobs: []shell3.JobInfo{{ID: "a"}, {ID: "b"}}}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.bgCount = 2
	// A notice set through Update gets its display window stamped.
	m.Update(shellDoneMsg{cmd: "echo hi"})
	// The structured snapshot's footer/notice fields are plain text — no ANSI
	// stripping needed — and independently verify the same facts the rendered
	// footer string carries.
	snap := m.uiSnapshot()
	if !strings.Contains(strings.Join(snap.footer, " "), "bg: 2") {
		t.Fatalf("snapshot footer should show the subprocess count as bg: N: %v", snap.footer)
	}
	if snap.notice != "! echo hi" {
		t.Fatalf("snapshot notice should be the last-action notice, got %q", snap.notice)
	}
	plain := stripANSI(m.renderFooter())
	if !strings.Contains(plain, "bg: 2") {
		t.Fatalf("footer should show the subprocess count as bg: N:\n%s", plain)
	}
	if !strings.Contains(plain, "echo hi") {
		t.Fatalf("footer should show the last-action notice:\n%s", plain)
	}
}

func TestFooterBgCountPoll(t *testing.T) {
	fc := &fakeCmds{jobs: []shell3.JobInfo{{ID: "a"}}}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// The poll refreshes the cached count from Jobs().
	m.Update(bgPollTickMsg{})
	if m.bgCount != 1 {
		t.Fatalf("poll should refresh bgCount to 1, got %d", m.bgCount)
	}
	if plain := stripANSI(m.renderFooter()); !strings.Contains(plain, "bg: 1") {
		t.Fatalf("footer should show bg: 1:\n%s", plain)
	}
}

// A zero subprocess count drops the pill entirely (no "bg: 0" clutter).
func TestFooterHidesZeroBgCount(t *testing.T) {
	m := sized(closedSend(nil))
	m.bgCount = 0
	if plain := stripANSI(m.renderFooter()); strings.Contains(plain, "bg:") {
		t.Fatalf("footer should not show a bg pill at zero:\n%s", plain)
	}
}

func TestFooterNoticeFadesAfterTTL(t *testing.T) {
	m := sized(closedSend(nil))
	m.Update(shellDoneMsg{cmd: "echo hi"})
	if !strings.Contains(stripANSI(m.renderFooter()), "echo hi") {
		t.Fatal("notice should show right after it is set")
	}
	// Simulate the display window elapsing.
	m.noticeAt = m.noticeAt.Add(-noticeTTL - time.Second)
	if strings.Contains(stripANSI(m.renderFooter()), "echo hi") {
		t.Fatal("notice should fade after noticeTTL")
	}
}
