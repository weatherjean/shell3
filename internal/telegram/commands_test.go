//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	fired := ""
	b.SetJobRunner(func(name string) error { fired = name; return nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run nightly"})
	if fired != "nightly" {
		t.Fatalf("expected /run to fire job 'nightly', fired %q", fired)
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nightly") {
		t.Fatalf("expected an ack mentioning the job, got %v", fc.sentTexts())
	}
}

func TestCommand_RunNoRunner(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}

// /run with no argument replies with usage instead of calling the runner —
// the usage text wraps <job> in a code span (like /cancel's usage) so the
// markdown->HTML renderer doesn't swallow it as a bogus tag.
func TestCommand_RunMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobRunner(func(name string) error { called = true; return nil })

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run"})
	if called {
		t.Fatal("run must not be invoked with no job name")
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "usage: /run") {
		t.Fatalf("expected the usage reply, got %v", fc.sentTexts())
	}
}

func TestCommand_Reload(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetReloader(func() (shell3.ReloadResult, error) {
		called = true
		return shell3.ReloadResult{Agents: 3, Jobs: 1}, nil
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !called {
		t.Fatal("expected /reload to invoke the reloader")
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reloaded") {
		t.Fatalf("expected a success reply, got %v", fc.sentTexts())
	}
}

func TestCommand_ReloadNoReloader(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

// TestCommand_UnknownDropped pins that a removed command (/clear, /set, …) is no
// longer routed — the trimmed command set answers "unknown command".
func TestCommand_UnknownDropped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
		t.Fatalf("expected /clear to be an unknown command now, got %v", fc.sentTexts())
	}
}

// /jobs lists whatever the wired list closure renders — the bot does no
// formatting of its own, it just relays the runtime's job-control surface.
func TestJobsCommandListsRunning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobControl(func() string {
		return "sub1  subagent  do the thing  2m14s"
	}, func(id string) error { return nil })

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/jobs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "sub1") || !strings.Contains(all, "2m14s") {
		t.Fatalf("expected the listing to be relayed, got %v", fc.sentTexts())
	}
}

// /jobs with nothing running relays the empty-state text.
func TestJobsCommandEmpty(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobControl(func() string {
		return "no background jobs running"
	}, func(id string) error { return nil })

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/jobs"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "no background jobs running") {
		t.Fatalf("expected the empty-state reply, got %v", fc.sentTexts())
	}
}

// /cancel <id> relays the cancel closure's confirmation.
func TestCancelCommandOK(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	cancelled := ""
	b.SetJobControl(func() string { return "" }, func(id string) error {
		cancelled = id
		return nil
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/cancel sub1"})
	if cancelled != "sub1" {
		t.Fatalf("expected /cancel to invoke cancel('sub1'), got %q", cancelled)
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "sub1") {
		t.Fatalf("expected a confirmation mentioning the id, got %v", fc.sentTexts())
	}
}

// /cancel with an unknown id surfaces the runtime's "no such task" error text
// verbatim — no bot-side reinterpretation.
func TestCancelCommandUnknownID(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetJobControl(func() string { return "" }, func(id string) error {
		return fmt.Errorf("no such task %q", id)
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/cancel bogus"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, `no such task "bogus"`) {
		t.Fatalf("expected the no-such-task error text, got %v", fc.sentTexts())
	}
}

// /cancel with no argument replies with usage instead of calling cancel.
func TestCancelCommandMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobControl(func() string { return "" }, func(id string) error {
		called = true
		return nil
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/cancel"})
	if called {
		t.Fatal("cancel must not be invoked with no id")
	}
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "usage: /cancel") {
		t.Fatalf("expected the usage reply, got %v", fc.sentTexts())
	}
}
