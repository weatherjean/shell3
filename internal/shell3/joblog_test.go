package shell3

import (
	"os"
	"strings"
	"testing"
)

// TestCommandJobWritesLogFile verifies a bash_bg command job tees its output
// to runs/<parent-session>/jobs/<id>.log so the notifier (and task_status) can
// read the full output after the in-memory ring has capped.
func TestCommandJobWritesLogFile(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startCommand(parent, "echo logged", t.TempDir(), []string{"echo", "logged"}, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	rt.jobs.wait()
	rt.jobs.mu.Lock()
	logPath := rt.jobs.jobs[id].logPath
	rt.jobs.mu.Unlock()
	if logPath == "" {
		t.Fatal("no logPath recorded on the job")
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read job log: %v", err)
	}
	if !strings.Contains(string(b), "logged") {
		t.Fatalf("log content = %q", b)
	}
	// task_status points at the full log.
	if st := rt.jobs.formatJobStatus(id); !strings.Contains(st, logPath) {
		t.Fatalf("task_status should name the log path, got: %q", st)
	}
}

// TestCommandJobLogCapped verifies the on-disk log stops growing at its cap
// instead of filling the disk.
func TestCommandJobLogCapped(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// ~2 MiB of output against a 1 MiB cap.
	id, err := rt.jobs.startCommand(parent, "yes", t.TempDir(),
		[]string{"sh", "-c", "head -c 2097152 /dev/zero | tr '\\0' 'x'"}, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	rt.jobs.wait()
	rt.jobs.mu.Lock()
	logPath := rt.jobs.jobs[id].logPath
	rt.jobs.mu.Unlock()
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > jobLogMaxBytes+1024 {
		t.Fatalf("log size %d exceeds cap %d", fi.Size(), jobLogMaxBytes)
	}
}

// TestCommandJobNoParentNoLog: a parentless (unit-test style) job records no
// log path and does not crash.
func TestCommandJobNoParentNoLog(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo hi", t.TempDir(), []string{"echo", "hi"}, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[id].logPath != "" {
		t.Fatalf("unexpected logPath %q", m.jobs[id].logPath)
	}
}
