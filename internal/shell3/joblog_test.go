package shell3

import (
	"os"
	"strings"
	"testing"
)

func TestCommandJobWritesLogFile(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startCommand(parent, "echo logged", t.TempDir(), []string{"echo", "logged"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rt.jobs.mu.Lock()
	logPath := rt.jobs.jobs[id].logPath
	rt.jobs.mu.Unlock()
	rt.jobs.wait()
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
}

func TestCommandJobLogCapped(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startCommand(parent, "yes", t.TempDir(),
		[]string{"sh", "-c", "head -c 2097152 /dev/zero | tr '\\0' 'x'"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rt.jobs.mu.Lock()
	logPath := rt.jobs.jobs[id].logPath
	rt.jobs.mu.Unlock()
	rt.jobs.wait()
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > jobLogMaxBytes+1024 {
		t.Fatalf("log size %d exceeds cap %d", fi.Size(), jobLogMaxBytes)
	}
}

func TestCommandJobNoParentNoLog(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo hi", t.TempDir(), []string{"echo", "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	logPath := m.jobs[id].logPath
	m.mu.Unlock()
	m.wg.Wait()
	if logPath != "" {
		t.Fatalf("unexpected logPath %q", logPath)
	}
}
