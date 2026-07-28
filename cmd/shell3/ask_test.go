//go:build unix

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	huh "charm.land/huh/v2"
)

// TestAskResumeHint pins the exact resume line printed on exit — it is the
// user's only pointer back into the conversation, so its wording (and the
// `shell3 ask --resume` invocation) is load-bearing.
func TestAskResumeHint(t *testing.T) {
	got := askResumeHint()
	if !strings.Contains(got, "shell3 ask --resume") {
		t.Errorf("resume hint missing the resume invocation: %q", got)
	}
	if strings.Contains(got, "shell3 dev") {
		t.Errorf("resume hint still references the old command name: %q", got)
	}
}

// TestErrAskAborted verifies the sentinel is distinct and matches via
// errors.Is — the loop relies on this to tell ctrl+c from a real error.
func TestErrAskAborted(t *testing.T) {
	if !errors.Is(errAskAborted, errAskAborted) {
		t.Fatal("errAskAborted must match itself via errors.Is")
	}
	if errors.Is(errAskAborted, huh.ErrUserAborted) {
		t.Error("errAskAborted must be distinct from huh.ErrUserAborted")
	}
}

// TestInteractiveAsk pins the asker-attachment decision: an interactive confirm
// prompt attaches ONLY when a human is plausibly present (no -p, TTY on both
// ends). -p (scripted) or a missing TTY means no asker — the Session then
// treats the hook ask as headless and auto-denies (the Q4 fix).
func TestInteractiveAsk(t *testing.T) {
	cases := []struct {
		name                          string
		scripted, stdinTTY, stderrTTY bool
		want                          bool
	}{
		{"interactive tty", false, true, true, true},
		{"scripted -p even with tty", true, true, true, false},
		{"no stdin tty (piped)", false, false, true, false},
		{"no stderr tty", false, true, false, false},
		{"scripted and no tty", true, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := interactiveAsk(c.scripted, c.stdinTTY, c.stderrTTY); got != c.want {
				t.Errorf("interactiveAsk(%v,%v,%v) = %v, want %v", c.scripted, c.stdinTTY, c.stderrTTY, got, c.want)
			}
		})
	}
}

// TestConfirmAsk verifies the interactive y/n prompt: only an explicit yes
// allows; everything else — including EOF / empty input — denies, and the
// reason and command are surfaced so the human decides informed.
func TestConfirmAsk(t *testing.T) {
	cases := []struct {
		name, input string
		want        bool
	}{
		{"y", "y\n", true},
		{"yes", "yes\n", true},
		{"uppercase Y", "Y\n", true},
		{"n", "n\n", false},
		{"blank", "\n", false},
		{"eof no input", "", false},
		{"other", "maybe\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var w strings.Builder
			got := confirmAsk(context.Background(), strings.NewReader(c.input), &w, "rm -rf /tmp/x", "destructive command")
			if got != c.want {
				t.Errorf("confirmAsk(%q) = %v, want %v", c.input, got, c.want)
			}
			if !strings.Contains(w.String(), "destructive command") {
				t.Errorf("prompt should surface the reason; got %q", w.String())
			}
			if !strings.Contains(w.String(), "rm -rf /tmp/x") {
				t.Errorf("prompt should surface the command; got %q", w.String())
			}
		})
	}
}
