package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

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

	if err := oldStore.Close(); err != nil {
		t.Fatalf("close old store: %v", err)
	}

	if err := s.sess.RestoreReminders(); err != nil {
		t.Fatalf("session store was not repointed at the new generation: %v", err)
	}
}
