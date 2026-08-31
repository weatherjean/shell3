package shell3

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/notify"
)

// fakeReloadState builds a reloadState that drives applyReload with the same
// fakellm-backed per-session config the test runtime already uses, plus an
// observable cleanup. It stands in for BuildParts so the generation lifecycle
// (park / drain / immediate-close) can be exercised without a real config dir.
func fakeReloadState(rt *Runtime, mk func() chat.Config, cleanup func()) reloadState {
	return reloadState{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			cfg := mk()
			cfg.Headless = o.Headless
			if o.WorkDir != "" {
				cfg.WorkDir = o.WorkDir
			}
			if cfg.Store == nil {
				cfg.Store = rt.store
			}
			return cfg, nil
		},
		cleanup:       cleanup,
		store:         rt.store,
		maxConcurrent: rt.jobs.max,
		agents:        1,
		models:        1,
	}
}

func closedWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// isClosed reports whether ch is already closed (non-blocking).
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestReloadProceedsWhileJobRunning: a reload no longer refuses while a
// background bash_bg job is live — it succeeds, the job completes on its own
// generation, and its completion notice still wakes the parent.
func TestReloadProceedsWhileJobRunning(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// A slow job so it is definitely still running across the reload.
	id, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	res, err := rt.applyReload(fakeReloadState(rt, fakeCfg("x"), func() {}))
	if err != nil {
		t.Fatalf("reload while a job runs must succeed, got: %v", err)
	}
	if res.Agents != 1 {
		t.Fatalf("reload result = %+v, want 1 agent", res)
	}

	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForWake(t, rt, parent)
	rt.jobs.wait()
}

// TestOldPartsCloseAfterDrain: a reload during a running bash_bg parks the old
// generation's closer, which runs only once the job drains — never before.
func TestOldPartsCloseAfterDrain(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// Make the CURRENT (old) generation's teardown observable before reloading.
	oldClosed := make(chan struct{})
	rt.cleanup = func() { close(oldClosed) }

	id, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	if _, err := rt.applyReload(fakeReloadState(rt, fakeCfg("x"), func() {})); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if isClosed(oldClosed) {
		t.Fatal("old generation closed while a job was still running (should be parked)")
	}

	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rt.jobs.wait()
	if !closedWithin(oldClosed, 3*time.Second) {
		t.Fatal("old generation never closed after the job drained")
	}
}

// TestDoubleReloadWhileLingering: two reloads while a job lingers park two old
// generations; both close once the job drains, and the newest generation stays
// live throughout.
func TestDoubleReloadWhileLingering(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	gen0 := make(chan struct{})
	rt.cleanup = func() { close(gen0) }

	id, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	// Reload 1: gen0 → gen1 (parks gen0's closer, job still running).
	gen1 := make(chan struct{})
	if _, err := rt.applyReload(fakeReloadState(rt, fakeCfg("x"), func() { close(gen1) })); err != nil {
		t.Fatalf("reload 1: %v", err)
	}
	// Reload 2: gen1 → gen2 (parks gen1's closer too, job still running).
	gen2 := make(chan struct{})
	if _, err := rt.applyReload(fakeReloadState(rt, fakeCfg("x"), func() { close(gen2) })); err != nil {
		t.Fatalf("reload 2: %v", err)
	}

	if isClosed(gen0) || isClosed(gen1) {
		t.Fatal("an old generation closed while the job was still running")
	}
	if isClosed(gen2) {
		t.Fatal("the newest (live) generation must not be closed")
	}

	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rt.jobs.wait()
	if !closedWithin(gen0, 3*time.Second) {
		t.Fatal("gen0 never closed after the job drained")
	}
	if !closedWithin(gen1, 3*time.Second) {
		t.Fatal("gen1 never closed after the job drained")
	}
	if isClosed(gen2) {
		t.Fatal("the newest generation closed after a drain — it must stay live")
	}
}
