package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestReloadRepointsSessionStore covers the reload bug at the runtime level:
// an idle front-end session surviving applyReload must have its chat.Session
// sidecar store handle repointed at the NEW generation's store, not left on
// the old one (which closes once the parked generation drains). fakeReloadState
// reuses rt.store for every call in the other reload tests, which can't
// distinguish "swapped" from "never touched" — this test drives applyReload
// with a distinct second store to make the swap observable, then closes the
// old store and confirms the session's sess now reads through the new one.
func TestReloadRepointsSessionStore(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	oldStore := rt.store

	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	newStore, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	t.Cleanup(func() { _ = newStore.Close() })

	if _, err := rt.applyReload(reloadState{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			cfg := fakeCfg("x")()
			cfg.Headless = o.Headless
			cfg.Store = newStore
			return cfg, nil
		},
		cleanup:       func() {},
		store:         newStore,
		maxConcurrent: rt.jobs.max,
		agents:        1,
		models:        1,
	}); err != nil {
		t.Fatalf("applyReload: %v", err)
	}

	// The old store closes (stands in for the parked generation's drain).
	if err := oldStore.Close(); err != nil {
		t.Fatalf("close old store: %v", err)
	}

	// If s.sess still held the old (now-closed) store, this would error.
	if err := s.sess.RestoreReminders(); err != nil {
		t.Fatalf("session store was not repointed at the new generation: %v", err)
	}
}
