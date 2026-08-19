//go:build unix

package telegram

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	fired := ""
	b.SetJobRunner(func(name string) error { fired = name; return nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run nightly"})
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
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run x"})
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

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run"})
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
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
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
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
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
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
		t.Fatalf("expected /clear to be an unknown command now, got %v", fc.sentTexts())
	}
}

// The view commands moved to the web dash: they answer "unknown command".
func TestViewCommandsRemoved(t *testing.T) {
	for _, cmd := range []string{"/status", "/jobs", "/job x", "/runs", "/cancel bg1", "/run_1", "/job_1", "/cancel_1"} {
		fc := newFakeClient()
		rt, _ := newFakeRuntime(t, "ok")
		b := newBot(t, fc, rt)
		b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: cmd})
		if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
			t.Errorf("%s: expected unknown command, got %v", cmd, fc.sentTexts())
		}
	}
}

// BotCommands is the surviving action set — no view commands, /dash and
// /superstop present.
func TestBotCommandsSurvivingSet(t *testing.T) {
	var names []string
	for _, c := range BotCommands() {
		names = append(names, c.Command)
	}
	got := strings.Join(names, " ")
	for _, want := range []string{"dash", "stop", "superstop", "new", "run", "btw", "reload", "quiet"} {
		if !strings.Contains(got, want) {
			t.Errorf("BotCommands missing %q: %v", want, names)
		}
	}
	for _, gone := range []string{"status", "jobs", "job", "cancel", "runs"} {
		for _, n := range names {
			if n == gone {
				t.Errorf("BotCommands still lists removed %q", gone)
			}
		}
	}
}

// Bare /dash with no listener wired explains itself.
func TestDashCommand_Disabled(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/dash"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "not running") {
		t.Fatalf("expected the disabled explanation, got %v", fc.sentTexts())
	}
}

// Bare /dash replies with the minted URL and the TTL hint.
func TestDashCommand_MintsURL(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetDash(func() (string, error) { return "http://127.0.0.1:7333/?t=abc123", nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/dash"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "http://127.0.0.1:7333/?t=abc123") || !strings.Contains(all, "~1h") {
		t.Fatalf("expected the tokened URL + TTL hint, got %v", fc.sentTexts())
	}
}

// /dash with an argument becomes a normal agent turn pointed at the
// dash-exposing skill — the agent's reply reaches the chat.
func TestDashCommand_ArgBecomesAgentTurn(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "tunnel is up")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/dash help exposing"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "tunnel is up")
	})
}

// /superstop with nothing running says so.
func TestSuperstop_NothingRunning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nothing was running") {
		t.Fatalf("expected the idle reply, got %v", fc.sentTexts())
	}
}

// /superstop kills a live background job, replies with ONE summary naming it,
// and queues the same summary into the conversation without waking it.
func TestSuperstop_KillsJobsAndSummarizes(t *testing.T) {
	fc := newFakeClient()
	blocking := fakellm.NewBlocking()
	rt := storeRuntimeClient(t, blocking)
	b := newBot(t, fc, rt)
	sess, err := b.mainSession()
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "spin forever", shell3.DispatchOpts{Description: "spinner"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, j := range sess.Jobs() {
			if j.ID == id && !j.Done {
				return true
			}
		}
		return false
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "superstop") || !strings.Contains(all, id) {
		t.Fatalf("expected one summary naming %s, got %v", id, fc.sentTexts())
	}
	waitFor(t, func() bool {
		for _, j := range sess.Jobs() {
			if j.ID == id && !j.Done {
				return false
			}
		}
		return true
	})
	if !sess.HasQueuedInput() {
		t.Error("expected the superstop summary queued (unwoken) in the conversation")
	}
}

func TestCommand_BotnameSuffixIsStripped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload@my_shell3_bot"})
	joined := strings.Join(fc.sentTexts(), "\n")
	if strings.Contains(joined, "unknown command") {
		t.Fatalf("/reload@botname must route to /reload, got %v", fc.sentTexts())
	}
	if !strings.Contains(joined, "reload not available") {
		t.Fatalf("expected the /reload reply, got %v", fc.sentTexts())
	}
}

// /quiet reports and flips the persisted toggle; junk gets usage.
func TestQuietCommand(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	qs := &QuietStore{Path: filepath.Join(t.TempDir(), "quiet_mode.json")}
	b.SetQuiet(qs)
	ctx := context.Background()

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is off")
	})

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet on"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is on")
	})
	if !qs.Get() {
		t.Error("/quiet on did not persist")
	}

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet off"})
	waitFor(t, func() bool {
		return strings.Count(strings.Join(fc.sentTexts(), "\n"), "quiet is off") >= 2
	})
	if qs.Get() {
		t.Error("/quiet off did not persist")
	}

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet sideways"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "usage: /quiet on|off")
	})
	if qs.Get() {
		t.Error("junk arg flipped the store")
	}
}

// A /dash link pointing off-machine (a planted or tunnel host) is flagged:
// the reply names the host and warns, so a prompt-injected dash_url.txt can't
// quietly hand out a live token to an attacker's server.
func TestDashCommand_WarnsOnNonLoopbackHost(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetDash(func() (string, error) { return "http://evil.example.com/?t=abc", nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/dash"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "evil.example.com") || !strings.Contains(all, "not this machine") {
		t.Fatalf("expected an off-machine warning, got %v", fc.sentTexts())
	}
}

// A loopback link carries no warning — the common case stays quiet.
func TestDashCommand_LoopbackNoWarning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetDash(func() (string, error) { return "http://127.0.0.1:7333/?t=abc", nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/dash"})
	if strings.Contains(strings.Join(fc.sentTexts(), "\n"), "not this machine") {
		t.Fatalf("loopback link should not warn, got %v", fc.sentTexts())
	}
}
