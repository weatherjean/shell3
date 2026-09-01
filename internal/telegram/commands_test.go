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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run nightly"})
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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}

func TestCommand_RunMissingArg(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetJobRunner(func(name string) error { called = true; return nil })

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/run"})
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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

func TestCommand_UnknownDropped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
		t.Fatalf("expected /clear to be an unknown command now, got %v", fc.sentTexts())
	}
}

func TestViewCommandsRemoved(t *testing.T) {
	for _, cmd := range []string{"/dash", "/jobs", "/job x", "/runs", "/cancel bg1", "/run_1", "/job_1", "/cancel_1"} {
		fc := newFakeClient()
		rt, _ := newFakeRuntime(t, "ok")
		b := newBot(t, fc, rt)
		tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: cmd})
		if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unknown command") {
			t.Errorf("%s: expected unknown command, got %v", cmd, fc.sentTexts())
		}
	}
}

func TestBotCommandsSurvivingSet(t *testing.T) {
	var names []string
	for _, c := range BotCommands() {
		names = append(names, c.Command)
	}
	got := strings.Join(names, " ")
	for _, want := range []string{"status", "stop", "superstop", "new", "run", "btw", "reload", "quiet"} {
		if !strings.Contains(got, want) {
			t.Errorf("BotCommands missing %q: %v", want, names)
		}
	}
	for _, gone := range []string{"dash", "jobs", "job", "cancel", "runs"} {
		for _, n := range names {
			if n == gone {
				t.Errorf("BotCommands still lists removed %q", gone)
			}
		}
	}
}

func TestStatusCommand_Unavailable(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/status"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "unavailable") {
		t.Fatalf("expected the unavailable explanation, got %v", fc.sentTexts())
	}
}

func TestStatusCommand_SendsDocument(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetStatusDocument(func(*shell3.Session) (string, []byte, error) {
		return "shell3-status.html", []byte("<!doctype html><p>ok</p>"), nil
	})
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/status"})
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.docs) != 1 || fc.docs[0].filename != "shell3-status.html" || !strings.Contains(string(fc.docs[0].data), "<p>ok</p>") {
		t.Fatalf("status document = %+v", fc.docs)
	}
}

func TestSuperstop_NothingRunning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nothing was running") {
		t.Fatalf("expected the idle reply, got %v", fc.sentTexts())
	}
}

func TestSuperstop_KillsJobsAndSummarizes(t *testing.T) {
	fc := newFakeClient()
	blocking := fakellm.NewBlocking()
	rt := storeRuntimeClient(t, blocking)
	b := newBot(t, fc, rt)
	sess, err := tconv(b).mainSession()
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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
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
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload@my_shell3_bot"})
	joined := strings.Join(fc.sentTexts(), "\n")
	if strings.Contains(joined, "unknown command") {
		t.Fatalf("/reload@botname must route to /reload, got %v", fc.sentTexts())
	}
	if !strings.Contains(joined, "reload not available") {
		t.Fatalf("expected the /reload reply, got %v", fc.sentTexts())
	}
}

func TestQuietCommand(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	qs := &QuietStore{Path: filepath.Join(t.TempDir(), "quiet_mode.json")}
	b.SetQuiet(qs)
	ctx := context.Background()

	tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is off")
	})

	tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet on"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "quiet is on")
	})
	if !qs.Get() {
		t.Error("/quiet on did not persist")
	}

	tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet off"})
	waitFor(t, func() bool {
		return strings.Count(strings.Join(fc.sentTexts(), "\n"), "quiet is off") >= 2
	})
	if qs.Get() {
		t.Error("/quiet off did not persist")
	}

	tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/quiet sideways"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "usage: /quiet on|off")
	})
	if qs.Get() {
		t.Error("junk arg flipped the store")
	}
}
