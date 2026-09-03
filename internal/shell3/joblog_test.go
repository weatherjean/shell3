package shell3

import (
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

func TestCommandJobWritesLogFile(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startCommand(parent, "echo logged", t.TempDir(), []string{"echo", "logged"}, nil, notify.ReportAuto, "")
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
}

func TestCommandJobLogCapped(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startCommand(parent, "yes", t.TempDir(),
		[]string{"sh", "-c", "head -c 2097152 /dev/zero | tr '\\0' 'x'"}, nil, notify.ReportAuto, "")
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

func TestCommandJobNoParentNoLog(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo hi", t.TempDir(), []string{"echo", "hi"}, nil, notify.ReportAuto, "")
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
