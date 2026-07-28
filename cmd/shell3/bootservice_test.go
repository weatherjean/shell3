//go:build unix

package main

import (
	"strings"
	"testing"
)

// TestServiceUnitRunsTheBot pins what the installed unit actually starts: the
// Telegram front-end, against the config dir boot just wrote. A unit pointing
// at another command is the difference between a bot that answers and one that
// crash-loops in the journal.
func TestServiceUnitRunsTheBot(t *testing.T) {
	unit := serviceUnit("/usr/local/bin/shell3", "/home/u/.shell3", "/home/u")
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/shell3 telegram --config /home/u/.shell3") {
		t.Errorf("unit does not start the telegram front-end:\n%s", unit)
	}
	if !strings.Contains(unit, "Restart=always") {
		t.Errorf("unit does not restart on crash:\n%s", unit)
	}
}

// TestWaitServiceActive covers the three shapes: immediately active, active
// after a few polls, and never active (a crash-looping unit must not be
// reported as running).
func TestWaitServiceActive(t *testing.T) {
	sleeps := 0
	sleep := func() { sleeps++ }

	always := func(...string) (string, error) { return "active", nil }
	if !waitServiceActive(always, 3, sleep) {
		t.Error("active unit reported as not running")
	}

	calls := 0
	eventually := func(...string) (string, error) {
		calls++
		if calls >= 3 {
			return "active", nil
		}
		return "activating", nil
	}
	if !waitServiceActive(eventually, 6, sleep) {
		t.Error("unit that becomes active reported as not running")
	}

	never := func(...string) (string, error) { return "failed", nil }
	if waitServiceActive(never, 4, sleep) {
		t.Error("failed unit reported as running")
	}
	if sleeps == 0 {
		t.Error("polling never slept between tries")
	}
}
