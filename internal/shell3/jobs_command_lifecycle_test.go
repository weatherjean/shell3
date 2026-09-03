package shell3

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestRuntimeCloseJoinsLiveCommandJob(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if _, err := rt.jobs.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- rt.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Runtime.Close hung with a live command job")
	}
}
