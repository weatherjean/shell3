//go:build unix

package main

import (
	"errors"
	"strings"
	"testing"

	huh "charm.land/huh/v2"
)

// TestEchoPrompt verifies the interactive-only re-echo of the sent message:
// huh's input form clears its own line on submit, so without this the user's
// message never appears in the terminal transcript. Only askPrompt's TTY call
// sites use it — argv/-p mode is unchanged, since the shell already echoed
// the command line.
func TestEchoPrompt(t *testing.T) {
	var w strings.Builder
	echoPrompt(&w, "list the files here")
	got := w.String()
	if !strings.Contains(got, "list the files here") {
		t.Errorf("echoPrompt should contain the sent message; got %q", got)
	}
	if !strings.Contains(got, "you") {
		t.Errorf("echoPrompt should label the line as the user's own message; got %q", got)
	}
}

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
			if got := interactiveTTY(c.scripted, c.stdinTTY, c.stderrTTY); got != c.want {
				t.Errorf("interactiveTTY(%v,%v,%v) = %v, want %v", c.scripted, c.stdinTTY, c.stderrTTY, got, c.want)
			}
		})
	}
}
