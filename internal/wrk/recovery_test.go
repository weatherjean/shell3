//go:build unix

package wrk

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
)

func TestRunReferenceRejectsTraversal(t *testing.T) {
	for _, ref := range []string{"../outside", "task/..", "./run", "task/.", "..", "."} {
		if got, err := ResolveRun(t.TempDir(), ref); err == nil {
			t.Fatalf("accepted %q: %s", ref, got)
		}
	}
}

func TestWaitDeadlinePersistsAcrossBeats(t *testing.T) {
	_, dir := startCommandRun(t, `(task "waiter" (wait approval (timeout "1h") (for (event "go"))))`)
	if got, err := Beat(t.Context(), dir); err != nil || got.Status != "waiting" {
		t.Fatalf("%+v %v", got, err)
	}
	deadlinePath := filepath.Join(dir, "nodes", "approval", "deadline.json")
	var deadline time.Time
	if err := readJSON(deadlinePath, &deadline); err != nil || deadline.IsZero() {
		t.Fatalf("deadline=%v error=%v", deadline, err)
	}
	if err := writeJSON(deadlinePath, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := Beat(t.Context(), dir)
	if err != nil || got.Status != "failed" {
		t.Fatalf("expired wait: %+v %v", got, err)
	}
}

func TestWaitAcceptsTimelySignalOnLateBeat(t *testing.T) {
	_, dir := startCommandRun(t, `(task "waiter" (wait approval (timeout "1h") (for (event "go"))))`)
	if got, err := Beat(t.Context(), dir); err != nil || got.Status != "waiting" {
		t.Fatalf("%+v %v", got, err)
	}
	deadline := time.Now().Add(-time.Second)
	event := ExternalEvent{ID: "approval", Name: "go", Created: deadline.Add(-time.Second)}
	if err := writeJSON(filepath.Join(dir, "events", event.ID+".json"), event); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "nodes", "approval", "deadline.json"), deadline); err != nil {
		t.Fatal(err)
	}
	if got, err := Beat(t.Context(), dir); err != nil || got.Status != "completed" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestRouterRetriesTerminalNotice(t *testing.T) {
	control := t.TempDir()
	_, dir := startCommandRunWithOptions(t, `(task "done" (command work (run "true")))`, func(o *StartOptions) { o.NotifyTo = "main"; o.NotifyState = control })
	if err := os.MkdirAll(filepath.Join(control, "inbox"), 0700); err != nil {
		t.Fatal(err)
	}
	obstruction := filepath.Join(control, "inbox", "bWFpbg")
	if err := os.WriteFile(obstruction, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Beat(t.Context(), dir)
	if err == nil || result.Status != "completed" {
		t.Fatalf("setup: %+v %v", result, err)
	}
	if err := os.Remove(obstruction); err != nil {
		t.Fatal(err)
	}
	r := &Router{ctx: t.Context(), root: control}
	if err := r.drive("wrk:done/test-run"); err != nil {
		t.Fatal(err)
	}
	persisted, err := TerminalNoticePersisted(dir)
	if err != nil || !persisted {
		t.Fatalf("notice not retried: %v", err)
	}
}

func TestMalformedRouteIsIsolated(t *testing.T) {
	control := t.TempDir()
	_, _ = startCommandRunWithOptions(t, `(task "valid" (command work (run "true")))`, func(o *StartOptions) { o.NotifyState = control })
	path := filepath.Join(control, "wrk-routes", "broken.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	metadataPath := routePath(control, "wrk:bad/run")
	if err := writeJSON(metadataPath, route{Version: routeVersion + 1, Target: "wrk:bad/run"}); err != nil {
		t.Fatal(err)
	}
	targets, err := registeredTargets(control, applog.Noop{})
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets=%v err=%v", targets, err)
	}
	if _, err := os.Stat(path + ".invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metadataPath + ".invalid"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStartAdmitsOneRun(t *testing.T) {
	dir := t.TempDir()
	config, definition := filepath.Join(dir, "shell3.lisp"), filepath.Join(dir, "task.wrk.lisp")
	if err := os.WriteFile(config, []byte(`(shell3 (version 1))`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition, []byte(`(task "race" (command work (run "true")))`), 0600); err != nil {
		t.Fatal(err)
	}
	opts := StartOptions{StateRoot: filepath.Join(dir, "state"), RunID: "shared", Shell3Bin: "/bin/false"}
	ready := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); <-ready; _, err := Start(config, definition, opts); results <- err }()
	}
	close(ready)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("admitted %d runs", successes)
	}
	if _, err := Inspect(filepath.Join(opts.StateRoot, "race", opts.RunID)); err != nil {
		t.Fatal(err)
	}
	opts.RunID = "mismatch"
	opts.ConfigHash = "wrong"
	if _, err := Start(config, definition, opts); err == nil {
		t.Fatal("ignored config hash mismatch")
	}
}
