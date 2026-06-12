package bgjobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive returns true if pid responds to signal 0 (process exists and we
// can signal it). Used to assert detachment / liveness in tests.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitDead polls until pid is gone or timeout. Returns true if dead.
func waitDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !alive(pid)
}

func TestStart_writesLogAndExits(t *testing.T) {
	wd := t.TempDir()
	job, err := Start([]string{"bash", "-c", "echo hi-from-bg"}, "echo hi-from-bg", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "bg_") || len(job.ID) != 9 {
		t.Fatalf("bad id: %q", job.ID)
	}
	if job.PID <= 0 {
		t.Fatalf("bad pid: %d", job.PID)
	}
	// Wait for process to finish + log flush.
	waitDead(job.PID, 2*time.Second)
	data, err := os.ReadFile(job.Log)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hi-from-bg") {
		t.Fatalf("log missing output: %q", data)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
}

func TestStart_appendsToRegistry(t *testing.T) {
	wd := t.TempDir()
	j1, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(j1.Log); os.Remove(j2.Log) })

	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(reg.Jobs))
	}
	if reg.Jobs[0].ID != j1.ID || reg.Jobs[1].ID != j2.ID {
		t.Fatalf("ids: %v", reg.Jobs)
	}
}

func TestStart_detached(t *testing.T) {
	wd := t.TempDir()
	job, err := Start([]string{"bash", "-c", "sleep 30"}, "sleep 30", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-job.PID, syscall.SIGKILL)
		os.Remove(job.Log)
	})
	// Process should still be alive 200ms after Start returns.
	time.Sleep(200 * time.Millisecond)
	if !alive(job.PID) {
		t.Fatalf("process died prematurely: pid %d", job.PID)
	}
}

func TestStart_grandchildKillablyViaPgid(t *testing.T) {
	wd := t.TempDir()
	// `setsid bash` ensures the inner sleep inherits the pgid.
	job, err := Start([]string{"bash", "-c", "sleep 30 & wait"}, "sleep 30 & wait", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	if !alive(job.PID) {
		t.Fatalf("parent not alive")
	}
	// Kill entire process group; both bash and grandchild should die.
	if err := syscall.Kill(-job.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -pgid: %v", err)
	}
	if !waitDead(job.PID, 2*time.Second) {
		t.Fatalf("process group survived SIGKILL")
	}
}

func TestStart_corruptRegistryRecovers(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, ".shell3"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(wd, ".shell3", "bg.json")
	if err := os.WriteFile(bad, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatalf("start should recover from corrupt bg.json: %v", err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	baks := backupFiles(t, wd)
	if len(baks) != 1 {
		t.Fatalf("expected exactly one .bak backup, got %d: %v", len(baks), baks)
	}
	if got, err := os.ReadFile(baks[0]); err != nil {
		t.Fatalf("read backup: %v", err)
	} else if string(got) != "{not valid json" {
		t.Fatalf("backup did not preserve corrupt content: %q", got)
	}
	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != 1 {
		t.Fatalf("registry should hold 1 job, got %d", len(reg.Jobs))
	}
}

// backupFiles returns all bg.json*.bak files under <wd>/.shell3.
func backupFiles(t *testing.T, wd string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(wd, ".shell3", "bg.json.*.bak"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return matches
}

// TestStart_corruptRegistryBackupsAreUnique verifies that two successive
// corruption cycles produce two DISTINCT backup files: the second corruption
// must not clobber the first one's forensic copy. With the old fixed-name
// (bg.json.bak) scheme this left only a single backup, so this fails before
// the unique/timestamped-name fix and passes after.
func TestStart_corruptRegistryBackupsAreUnique(t *testing.T) {
	wd := t.TempDir()
	reg := filepath.Join(wd, ".shell3", "bg.json")
	if err := os.MkdirAll(filepath.Dir(reg), 0o755); err != nil {
		t.Fatal(err)
	}

	// First corruption cycle.
	if err := os.WriteFile(reg, []byte("first-corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	j1, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatalf("first recover: %v", err)
	}
	t.Cleanup(func() { os.Remove(j1.Log) })

	// Second corruption cycle: overwrite the freshly-written registry with
	// new garbage and append again.
	if err := os.WriteFile(reg, []byte("second-corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	j2, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	t.Cleanup(func() { os.Remove(j2.Log) })

	baks := backupFiles(t, wd)
	if len(baks) != 2 {
		t.Fatalf("want 2 distinct backups (no clobber), got %d: %v", len(baks), baks)
	}
	// Both corrupt payloads must be preserved across the two backups.
	seen := map[string]bool{}
	for _, b := range baks {
		data, err := os.ReadFile(b)
		if err != nil {
			t.Fatalf("read backup %s: %v", b, err)
		}
		seen[string(data)] = true
	}
	if !seen["first-corrupt"] || !seen["second-corrupt"] {
		t.Fatalf("backups did not preserve both corrupt payloads: %v", seen)
	}
}

// TestAppendJob_backupRenameFailureDoesNotOverwrite verifies that when the
// corrupt-registry backup rename fails, appendJob returns an error WITHOUT
// overwriting the corrupt original — so the bad data is not silently
// destroyed.
func TestAppendJob_backupRenameFailureDoesNotOverwrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0555 dirs are still writable, rename cannot be forced to fail")
	}
	wd := t.TempDir()
	reg := filepath.Join(wd, ".shell3", "bg.json")
	if err := os.MkdirAll(filepath.Dir(reg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg, []byte("corrupt-payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make .shell3 read-only so the os.Rename of the backup fails (EACCES /
	// EPERM): the directory entries cannot be modified.
	dir := filepath.Dir(reg)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := appendJob(wd, Job{ID: "bg_test"})
	if err == nil {
		t.Fatal("expected error when backup rename fails, got nil")
	}
	// The error must come from the backup step, proving appendJob bailed out
	// BEFORE attempting to overwrite. With the old code the rename error was
	// swallowed and the failure instead surfaced later from writeAtomic.
	if !strings.Contains(err.Error(), "back up corrupt bg.json") {
		t.Fatalf("error should report the failed backup, got: %v", err)
	}
	// Restore write perms so we can inspect, then confirm the corrupt
	// original is intact (not overwritten with a fresh registry).
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(reg)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != "corrupt-payload" {
		t.Fatalf("corrupt original was overwritten: %q", got)
	}
}

func TestStart_concurrentAppendsDoNotRace(t *testing.T) {
	wd := t.TempDir()
	const n = 10
	errs := make(chan error, n)
	jobs := make(chan Job, n)
	for i := 0; i < n; i++ {
		go func() {
			j, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
			jobs <- j
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	close(jobs)
	t.Cleanup(func() {
		for j := range jobs {
			os.Remove(j.Log)
		}
	})
	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != n {
		t.Fatalf("want %d jobs, got %d", n, len(reg.Jobs))
	}
}

func TestStart_emptyCommandRejected(t *testing.T) {
	if _, err := Start(nil, "", t.TempDir(), nil, "", true); err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestStart_killsProcessWhenPersistFails(t *testing.T) {
	wd := t.TempDir()
	// Force appendJob's MkdirAll(<wd>/.shell3) to fail by occupying that path
	// with a regular file, so Start spawns the process but cannot persist it.
	if err := os.WriteFile(filepath.Join(wd, ".shell3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(wd, "ran.marker")
	// If the spawned process is NOT killed, it survives the 1s sleep and creates
	// the marker. If Start kills it on persist failure, the marker never appears.
	cmd := fmt.Sprintf("sleep 1 && touch %q", marker)
	_, err := Start([]string{"bash", "-c", cmd}, cmd, wd, nil, "", true)
	if err == nil {
		t.Fatal("expected persist error (.shell3 is a file), got nil")
	}
	time.Sleep(2 * time.Second) // past the 1s sleep
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("spawned process was orphaned on persist failure (marker was created)")
	}
}

func TestKillAll(t *testing.T) {
	dir := t.TempDir()
	job, err := Start([]string{"bash", "-c", "sleep 60"}, "sleep 60", dir, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(job.PID, 0) != nil {
		t.Fatalf("job %d not alive after Start", job.PID)
	}
	n, err := KillAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("KillAll reported %d killed, want >=1", n)
	}
	if !waitDead(job.PID, 2*time.Second) {
		t.Errorf("job %d still alive after KillAll", job.PID)
	}
	if jobs, _ := LoadRegistry(dir); len(jobs.Jobs) != 0 {
		t.Errorf("registry not pruned: %v", jobs.Jobs)
	}
}

// waitSinkLines polls a sink file until it holds at least n complete lines or
// the timeout elapses, returning whatever lines it could read. The reaper
// appends bg_done asynchronously after Wait, so the test can't read the sink
// the instant Start returns.
func waitSinkLines(t *testing.T, path string, n int, timeout time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var out []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line == "" {
					continue
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("sink line not valid json: %q: %v", line, err)
				}
				out = append(out, m)
			}
			if len(out) >= n {
				return out
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("sink %s did not reach %d lines within %s", path, n, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStart_emitsBgDoneOnExit verifies the reaper appends a bg_done
// notification to the sink with the job id, exit code, log path, and command
// once the process exits.
func TestStart_emitsBgDoneOnExit(t *testing.T) {
	wd := t.TempDir()
	sinkPath := filepath.Join(wd, ".shell3", "sink", "main.jsonl")
	job, err := Start([]string{"bash", "-c", "exit 3"}, "exit 3", wd, nil, sinkPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })

	lines := waitSinkLines(t, sinkPath, 1, 3*time.Second)
	n := lines[0]
	if n["kind"] != "bg_done" {
		t.Fatalf("kind = %v, want bg_done", n["kind"])
	}
	if n["id"] != job.ID {
		t.Fatalf("id = %v, want %s", n["id"], job.ID)
	}
	// JSON numbers decode to float64.
	if exit, ok := n["exit"].(float64); !ok || int(exit) != 3 {
		t.Fatalf("exit = %v, want 3", n["exit"])
	}
	if n["log"] != job.Log {
		t.Fatalf("log = %v, want %s", n["log"], job.Log)
	}
	if n["cmd"] != "exit 3" {
		t.Fatalf("cmd = %v, want %q", n["cmd"], "exit 3")
	}
}

// TestStart_notifyOnExitFalseSkipsBgDone verifies that notifyOnExit=false
// suppresses the bg_done append even when a sinkPath is configured — the path a
// subagent spawn uses so its own agent_done is the only notification (no
// duplicate bg_done). The job still runs and is reaped; only the sink stays empty.
func TestStart_notifyOnExitFalseSkipsBgDone(t *testing.T) {
	wd := t.TempDir()
	sinkPath := filepath.Join(wd, ".shell3", "sink", "main.jsonl")
	job, err := Start([]string{"bash", "-c", "exit 0"}, "exit 0", wd, nil, sinkPath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })

	// Let the process exit and the reaper run, then assert no sink line landed.
	waitDead(job.PID, 2*time.Second)
	time.Sleep(150 * time.Millisecond)
	if data, err := os.ReadFile(sinkPath); err == nil && len(data) > 0 {
		t.Fatalf("notify_on_exit=false must write no bg_done, got sink content: %q", data)
	}
}

// TestStart_emptySinkPathSkipsNotification verifies an empty sinkPath is a safe
// no-op: the job still runs and is tracked, but no sink file is written.
func TestStart_emptySinkPathSkipsNotification(t *testing.T) {
	wd := t.TempDir()
	job, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	// Give the reaper a moment to run; it must NOT create a sink dir/file.
	waitDead(job.PID, time.Second)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(wd, ".shell3", "sink")); !os.IsNotExist(err) {
		t.Fatalf("sink dir should not exist with empty sinkPath, stat err: %v", err)
	}
}

func TestRegistry_jsonShape(t *testing.T) {
	wd := t.TempDir()
	job, err := Start([]string{"bash", "-c", "true"}, "true", wd, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	data, err := os.ReadFile(registryPath(wd))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("bg.json not valid json: %v", err)
	}
	if _, ok := raw["jobs"]; !ok {
		t.Fatalf("missing 'jobs' key: %s", data)
	}
}

func TestStartInjectsEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cmd := `printf "%s" "$GREETING" > ` + out
	job, err := Start([]string{"bash", "-c", cmd}, cmd, dir, []string{"GREETING=hi-env"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// wait for the detached job to finish writing
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(out); string(b) == "hi-env" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = job
	t.Fatalf("env var not visible to background job; out=%q", readFile(out))
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }
