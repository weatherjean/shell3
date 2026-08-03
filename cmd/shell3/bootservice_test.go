//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
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

// TestServiceStartDiagnosisReusesTelegramCheck pins the diagnosis on the
// front-end's own refusal: a boot that deferred the token writes a config
// `shell3 telegram` will not start on, and the service failure must say the
// same thing the front-end would, not a second guess at it.
func TestServiceStartDiagnosisReusesTelegramCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := &bootFlags{url: "http://localhost:9999/v1", model: "test-model", name: "main"}
	if err := runBoot(f); err != nil {
		t.Fatalf("runBoot: %v", err)
	}
	dir := filepath.Join(home, ".shell3")

	got := serviceStartDiagnosis(dir)
	_, err := telegramChatID(mustLoadTelegram(t, dir))
	if err == nil {
		t.Fatal("a boot with no token should leave the telegram wiring incomplete")
	}
	if !strings.Contains(got, err.Error()) {
		t.Errorf("diagnosis = %q, want it to carry the front-end's own refusal %q", got, err)
	}
}

func mustLoadTelegram(t *testing.T, dir string) config.TelegramConfig {
	t.Helper()
	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	defer c.Close()
	return c.Telegram()
}
