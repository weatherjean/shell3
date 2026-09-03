//go:build unix

package wrk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
)

func newRoutedWaitRun(t *testing.T, root, runID, output string) string {
	t.Helper()
	configPath := filepath.Join(root, runID+".shell3.lisp")
	definitionPath := filepath.Join(root, runID+".wrk.lisp")
	if err := os.WriteFile(configPath, []byte("(shell3 (version 1))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := `(task "routed"
  (wait approval (for (event "approved")))
  (command finish (after approval) (run "printf done > ` + output + `")))`
	if err := os.WriteFile(definitionPath, []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	runDir, err := Start(configPath, definitionPath, StartOptions{
		StateRoot: filepath.Join(root, "detached-state"), NotifyState: filepath.Join(root, "control"),
		RunID: runID, Shell3Bin: "/bin/false",
	})
	if err != nil {
		t.Fatal(err)
	}
	return runDir
}

func waitForRunStatus(t *testing.T, runDir, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := Inspect(runDir)
		if err == nil && snapshot.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshot, err := Inspect(runDir)
	t.Fatalf("run did not reach %s: snapshot=%+v err=%v", want, snapshot, err)
}

func TestRouterRecoversOfflineSignalFromRegistryWithCustomState(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-wrk-router-offline-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runDir := newRoutedWaitRun(t, root, "offline", "offline.txt")
	event, err := Signal(runDir, "approved", "sent while shell3 is down")
	if err != nil {
		t.Fatal(err)
	}
	if event.Wake != "unavailable" {
		t.Fatalf("offline wake = %q", event.Wake)
	}
	router, err := StartRouter(t.Context(), filepath.Join(root, "control"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	waitForRunStatus(t, runDir, "completed")
	if got, err := os.ReadFile(filepath.Join(root, "offline.txt")); err != nil || string(got) != "done" {
		t.Fatalf("output = %q, err=%v", got, err)
	}
}

func TestRouterUsesLiveListenerHint(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-wrk-router-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	control := filepath.Join(root, "control")
	store := inbox.Store{Root: control}
	listener, err := inbox.StartListener(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	router, err := StartRouter(t.Context(), control, listener.Hints(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	runDir := newRoutedWaitRun(t, root, "live", "live.txt")
	result, err := Beat(t.Context(), runDir)
	if err != nil || result.Status != "waiting" {
		t.Fatalf("initial beat = %+v, err=%v", result, err)
	}
	event, err := Signal(runDir, "approved", "sent to live shell3")
	if err != nil {
		t.Fatal(err)
	}
	if event.Wake != "delivered" {
		t.Fatalf("live wake = %q", event.Wake)
	}
	waitForRunStatus(t, runDir, "completed")
	if got, err := os.ReadFile(filepath.Join(root, "live.txt")); err != nil || string(got) != "done" {
		t.Fatalf("output = %q, err=%v", got, err)
	}
}

func TestRouterQuarantinesIncompatibleSnapshotAndReportsOnce(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-wrk-router-invalid-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runDir := newRoutedWaitRun(t, root, "old-schema", "unused.txt")
	oldConfig := `(shell3
  (version 1)
  (model primary (api-key-env TEST_KEY) (id "test"))
  (orchestrator (model primary) (instructions "removed field")))`
	if err := os.WriteFile(filepath.Join(runDir, "shell3.lisp"), []byte(oldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(root, "control")
	router, err := StartRouter(t.Context(), control, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Stat(filepath.Join(control, "errors.jsonl"))
		if err == nil && info.Size() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, total, err := (inbox.Store{Root: control}).List("main", inbox.StatusPending, 0, 10)
	if err != nil || total != 0 {
		t.Fatalf("invalid route created %d inbox notices, err=%v", total, err)
	}
	data, err := os.ReadFile(filepath.Join(control, "errors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("error log has %d records: %s", len(lines), data)
	}
	var record struct {
		Message string         `json:"message"`
		Error   string         `json:"error"`
		Fields  map[string]any `json:"fields"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	if record.Message != "workflow route failed" || record.Fields["target"] != "wrk:routed/old-schema" || !strings.Contains(record.Error, "immutable run snapshot hash mismatch") {
		t.Fatalf("error record = %+v", record)
	}
	targets, err := registeredTargets(control)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("invalid route remains active: %v", targets)
	}
	entries, err := os.ReadDir(filepath.Join(control, "wrk-routes"))
	if err != nil {
		t.Fatal(err)
	}
	var quarantined bool
	for _, entry := range entries {
		quarantined = quarantined || strings.HasSuffix(entry.Name(), ".json.invalid")
	}
	if !quarantined {
		t.Fatal("invalid route was not retained as a quarantined record")
	}
}

func TestRouterDeduplicatesRepeatedDiagnostic(t *testing.T) {
	root := t.TempDir()
	router, err := StartRouter(t.Context(), root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	target := "wrk:missing/run"
	for range 2 {
		router.enqueue(target)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			router.mu.Lock()
			active := router.active[target]
			router.mu.Unlock()
			if !active {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "errors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 1 {
		t.Fatalf("repeated error wrote %d records: %s", len(lines), data)
	}
}
