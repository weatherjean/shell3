package tui

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKeyVerdict(t *testing.T) {
	cases := []struct {
		b       byte
		yes     bool
		decided bool
	}{
		{'y', true, true},
		{'Y', true, true},
		{'n', false, true},
		{'N', false, true},
		{3, false, true},    // Ctrl-C → no
		{27, false, true},   // Esc → no
		{'\r', false, true}, // Enter → no (default)
		{'\n', false, true},
		{'x', false, false}, // ignored
		{' ', false, false},
		{'q', false, false},
	}
	for _, c := range cases {
		yes, decided := keyVerdict(c.b)
		if yes != c.yes || decided != c.decided {
			t.Errorf("keyVerdict(%d) = (%v,%v), want (%v,%v)", c.b, yes, decided, c.yes, c.decided)
		}
	}
}

// A pipe is not a terminal, so MakeRaw fails and confirmPrompt falls back to a
// line read — which lets us exercise the decision end-to-end in a test.
func TestConfirmPrompt_NonTTYFallback(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false}, // empty → no
		{"", false},   // EOF → no
		{"maybe\n", false},
	}
	for _, c := range cases {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func(s string) { io.WriteString(w, s); w.Close() }(c.in)
		got := confirmPrompt(context.Background(), r, io.Discard, "ls")
		r.Close()
		if got != c.want {
			t.Errorf("confirmPrompt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestConfirmPrompt_ShowsCommand(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { io.WriteString(w, "n\n"); w.Close() }()
	var buf strings.Builder
	confirmPrompt(context.Background(), r, &buf, "rm -rf foo")
	r.Close()
	if !strings.Contains(buf.String(), "rm -rf foo") {
		t.Errorf("prompt missing command; got %q", buf.String())
	}
}

// A cancelled ctx must unblock the prompt as a deny even when no key/line ever
// arrives (e.g. the bash_safety ask timeout fired while the user was away).
func TestConfirmPrompt_CtxCancelDenies(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- confirmPrompt(ctx, r, io.Discard, "ls") }() // r never receives input
	cancel()

	select {
	case got := <-done:
		if got {
			t.Fatal("cancelled prompt returned true, want false (deny)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirmPrompt did not return after ctx cancel")
	}
}
