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
	logPath := sessionStore(parent).JobLogPath(parent.ID(), id)
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
	logPath := sessionStore(parent).JobLogPath(parent.ID(), id)
	rt.jobs.wait()
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > jobLogMaxBytes+1024 {
		t.Fatalf("log size %d exceeds cap %d", fi.Size(), jobLogMaxBytes)
	}
}
