//go:build unix

package main

import "testing"

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
