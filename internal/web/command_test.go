//go:build unix

package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// commandDriver builds a live driver over a fake-LLM runtime for Command tests.
func commandDriver(t *testing.T) *Driver {
	t.Helper()
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	return NewDriver(context.Background(), rt, sess)
}

func TestCommandHelpListsEverything(t *testing.T) {
	d := commandDriver(t)
	help := d.Command("/help")
	for _, c := range []string{"/help", "/clear", "/compact", "/set", "/rollback", "/stop", "/run", "/reload"} {
		if !strings.Contains(help, c) {
			t.Errorf("/help missing %s:\n%s", c, help)
		}
	}
}

func TestCommandUnknown(t *testing.T) {
	d := commandDriver(t)
	got := d.Command("/nope")
	if !strings.Contains(got, "unknown command: /nope") || !strings.Contains(got, "/reload") {
		t.Fatalf("want unknown + help, got:\n%s", got)
	}
}

func TestCommandClear(t *testing.T) {
	d := commandDriver(t)
	d.drain(d.sess.Send(context.Background(), "hello")) // seed history
	if got := d.Command("/clear"); got != "🧹 cleared" {
		t.Fatalf("clear reply: %q", got)
	}
	if h := d.sess.History(); len(h) != 0 {
		t.Fatalf("history not cleared: %d entries", len(h))
	}
}

func TestCommandRollbackNothing(t *testing.T) {
	d := commandDriver(t)
	if got := d.Command("/rollback"); got != "nothing to roll back" {
		t.Fatalf("rollback reply: %q", got)
	}
}

func TestCommandStopIdle(t *testing.T) {
	d := commandDriver(t)
	if got := d.Command("/stop"); got != "nothing running" {
		t.Fatalf("stop reply: %q", got)
	}
}

func TestCommandRun(t *testing.T) {
	d := commandDriver(t)
	if got := d.Command("/run x"); got != "no scheduled jobs configured" {
		t.Fatalf("run without runner: %q", got)
	}
	var fired string
	d.SetJobRunner(func(name string) error { fired = name; return nil })
	if got := d.Command("/run"); got != "usage: /run <job>" {
		t.Fatalf("bare run: %q", got)
	}
	if got := d.Command("/run nightly"); got != "▶️ fired job nightly" || fired != "nightly" {
		t.Fatalf("run: reply %q, fired %q", got, fired)
	}
	d.SetJobRunner(func(string) error { return errors.New("boom") })
	if got := d.Command("/run nightly"); got != "run failed: boom" {
		t.Fatalf("run failure: %q", got)
	}
}

func TestCommandReload(t *testing.T) {
	d := commandDriver(t)
	if got := d.Command("/reload"); got != "reload not available" {
		t.Fatalf("reload without reloader: %q", got)
	}
	d.SetReloader(func() (shell3.ReloadResult, error) {
		return shell3.ReloadResult{Agents: 1, Models: 2, Jobs: 3, Notes: []string{"note"}}, nil
	})
	got := d.Command("/reload")
	if !strings.Contains(got, "✅ reloaded — 1 agents, 2 models, 3 jobs") || !strings.Contains(got, "note") {
		t.Fatalf("reload reply: %q", got)
	}
	d.SetReloader(func() (shell3.ReloadResult, error) { return shell3.ReloadResult{}, errors.New("bad lua") })
	if got := d.Command("/reload"); !strings.Contains(got, "reload failed: bad lua") {
		t.Fatalf("reload failure: %q", got)
	}
}

func TestCommandReloadRefusedMidTurn(t *testing.T) {
	d := commandDriver(t)
	d.SetReloader(func() (shell3.ReloadResult, error) { return shell3.ReloadResult{}, nil })
	if _, ok := d.takeSlot(); !ok {
		t.Fatal("could not take slot")
	}
	defer d.releaseSlot()
	if got := d.Command("/reload"); !strings.Contains(got, "turn is in flight") {
		t.Fatalf("busy reload: %q", got)
	}
}

func TestCommandSetBareLists(t *testing.T) {
	d := commandDriver(t)
	got := d.Command("/set")
	if !strings.Contains(got, "settable parameters") && !strings.Contains(got, "no settable parameters") {
		t.Fatalf("bare /set: %q", got)
	}
	if got := d.Command("/set only-a-name"); !strings.Contains(got, "usage: /set <name> <value>") {
		t.Fatalf("malformed /set: %q", got)
	}
}

func TestCommandSetWhitespace(t *testing.T) {
	d := commandDriver(t)
	// Double spaces and tabs between name and value must not corrupt either
	// (mobile keyboards insert them easily).
	got := d.Command("/set temperature \t 0.5")
	if !strings.Contains(got, "temperature = 0.5") && !strings.Contains(got, "set failed") {
		t.Fatalf("whitespace /set: %q", got)
	}
	if strings.Contains(got, "= \t") || strings.Contains(got, "=  ") {
		t.Fatalf("value kept leading whitespace: %q", got)
	}
}

// TestCommandCompact pins /compact end to end over the web driver: a fresh
// session reports nothing to compact; after a few chunky turns the command
// summarises (one quiet fakellm call) and reports the token delta.
func TestCommandCompact(t *testing.T) {
	d := commandDriver(t)

	if got := d.Command("/compact"); !strings.Contains(got, "nothing to compact") {
		t.Fatalf("fresh session: want 'nothing to compact', got %q", got)
	}

	// Grow history past the forced-compact tail floor (~4k tokens): three turns
	// with ~40KB prompts. The fake runtime's scripted "ok" replies serve as both
	// turn responses and, later, the compaction summary.
	big := strings.Repeat("x", 40000)
	for i := 0; i < 3; i++ {
		for range d.sess.Send(context.Background(), big) {
		}
	}
	got := d.Command("/compact")
	if !strings.Contains(got, "compacted:") || !strings.Contains(got, "→") {
		t.Fatalf("want a compacted delta reply, got %q", got)
	}
}
