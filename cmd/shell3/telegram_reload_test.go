//go:build unix

package main

import (
	"errors"
	"testing"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/web"
)

// fakeReloader stands in for *shell3.Runtime: Reload returns a scripted result,
// Cron returns the jobs the reloaded config would expose.
type fakeReloader struct {
	res     shell3.ReloadResult
	err     error
	jobs    []shell3.CronJob
	reloads int
}

func (f *fakeReloader) Reload() (shell3.ReloadResult, error) {
	f.reloads++
	return f.res, f.err
}
func (f *fakeReloader) Cron() []shell3.CronJob { return f.jobs }

// fakeBot records the rearm calls reloadAndRearm makes on the bot.
type fakeBot struct {
	redecorated int
	runnerSet   int
	runnerNil   bool
}

func (b *fakeBot) RedecorateSession() { b.redecorated++ }
func (b *fakeBot) SetJobRunner(fn func(name string) error) {
	b.runnerSet++
	b.runnerNil = fn == nil
}

// fakeDash records the dashboard's cron-source rewiring.
type fakeDash struct {
	sourceSet int
	sourceNil bool
}

func (d *fakeDash) SetCronSource(fn func() []web.CronJob) {
	d.sourceSet++
	d.sourceNil = fn == nil
}

// fakeDispatcher satisfies cron.Dispatcher so cron.New can build a real
// scheduler without a live session.
type fakeDispatcher struct{}

func (fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	return "sub-1", nil
}

// TestReloadAndRearm_ArmsNewScheduler pins the happy path: a reload whose config
// declares a cron job re-decorates the session, stops nothing (no prior
// scheduler), and arms exactly one new scheduler wired to both the bot's /run
// handler and the dashboard cron source.
func TestReloadAndRearm_ArmsNewScheduler(t *testing.T) {
	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}}}
	b := &fakeBot{}
	d := &fakeDash{}

	ns, _, err := reloadAndRearm(r, b, d, fakeDispatcher{}, nil)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	if ns == nil {
		t.Fatal("expected a new scheduler, got nil")
	}
	t.Cleanup(ns.Stop)

	if b.redecorated != 1 {
		t.Errorf("RedecorateSession called %d times, want 1", b.redecorated)
	}
	if b.runnerSet != 1 || b.runnerNil {
		t.Errorf("SetJobRunner set=%d nil=%v, want set=1 nil=false", b.runnerSet, b.runnerNil)
	}
	if d.sourceSet != 1 || d.sourceNil {
		t.Errorf("SetCronSource set=%d nil=%v, want set=1 nil=false", d.sourceSet, d.sourceNil)
	}
	if len(ns.Jobs()) != 1 {
		t.Errorf("new scheduler has %d jobs, want 1", len(ns.Jobs()))
	}
}

// TestReloadAndRearm_NoJobsClearsSchedule pins that reloading into a
// jobless config stops the prior scheduler, returns nil, and clears both the
// /run handler and the dashboard cron source.
func TestReloadAndRearm_NoJobsClearsSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()

	r := &fakeReloader{} // no jobs after reload
	b := &fakeBot{}
	d := &fakeDash{}

	ns, _, err := reloadAndRearm(r, b, d, fakeDispatcher{}, old)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	if ns != nil {
		ns.Stop()
		t.Fatalf("expected nil scheduler for jobless config, got %v", ns)
	}
	if b.redecorated != 1 {
		t.Errorf("RedecorateSession called %d times, want 1", b.redecorated)
	}
	if b.runnerSet != 1 || !b.runnerNil {
		t.Errorf("SetJobRunner set=%d nil=%v, want set=1 nil=true", b.runnerSet, b.runnerNil)
	}
	if d.sourceSet != 1 || !d.sourceNil {
		t.Errorf("SetCronSource set=%d nil=%v, want set=1 nil=true", d.sourceSet, d.sourceNil)
	}
}

// TestReloadAndRearm_ReloadErrorKeepsOldSchedule pins the fail-safe: a reload
// error leaves the running scheduler untouched (returned unchanged) and never
// re-decorates the session.
func TestReloadAndRearm_ReloadErrorKeepsOldSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()
	t.Cleanup(old.Stop)

	r := &fakeReloader{err: errors.New("bad config")}
	b := &fakeBot{}
	d := &fakeDash{}

	ns, _, err := reloadAndRearm(r, b, d, fakeDispatcher{}, old)
	if err == nil {
		t.Fatal("expected reload error")
	}
	if ns != old {
		t.Error("scheduler should be unchanged on reload failure")
	}
	if b.redecorated != 0 || b.runnerSet != 0 || d.sourceSet != 0 {
		t.Errorf("no rearm should happen on reload failure: redecorated=%d runnerSet=%d sourceSet=%d", b.redecorated, b.runnerSet, d.sourceSet)
	}
}

// TestReloadAndRearm_NilDashboard pins that a nil dashboard (no web UI) is
// tolerated — the rearm still wires the bot's /run handler.
func TestReloadAndRearm_NilDashboard(t *testing.T) {
	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}}}
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, nil, fakeDispatcher{}, nil)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	if ns == nil {
		t.Fatal("expected a new scheduler, got nil")
	}
	t.Cleanup(ns.Stop)
	if b.runnerSet != 1 || b.runnerNil {
		t.Errorf("SetJobRunner set=%d nil=%v, want set=1 nil=false", b.runnerSet, b.runnerNil)
	}
}

// A malformed-but-non-empty schedule passes config validation (validateCron
// doesn't parse expressions) and fails only at cron.New. The old scheduler
// must survive that failure — build-new-before-stop-old.
func TestReloadAndRearm_BadScheduleKeepsOldSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()
	t.Cleanup(old.Stop)

	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "bad", Schedule: "not a schedule", Agent: "explorer", Prompt: "p"}}}
	b := &fakeBot{}
	d := &fakeDash{}

	ns, _, err := reloadAndRearm(r, b, d, fakeDispatcher{}, old)
	if err == nil {
		t.Fatal("expected cron.New parse error")
	}
	if ns != old {
		t.Error("scheduler should be unchanged when the new schedule fails to arm")
	}
	// The surviving scheduler must still fire manually.
	if err := old.Run("j"); err != nil {
		t.Errorf("old scheduler should still be running: %v", err)
	}
}
