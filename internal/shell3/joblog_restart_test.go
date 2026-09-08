package shell3

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestJobLogsSurviveManagerRestart(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(manager *jobManager, text string) string {
		t.Helper()
		id, err := manager.startCommand(parent, text, t.TempDir(), []string{"/bin/echo", text}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^bg-[0-9a-f]{16}$`).MatchString(id) {
			t.Fatalf("unexpected job ID %q", id)
		}
		manager.wait()
		return sessionStore(parent).JobLogPath(parent.ID(), id)
	}
	first := run(rt.jobs, "first-job")
	second := run(newJobManager(rt, 0), "second-job")
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.Contains(string(data), "first-job") {
		t.Fatalf("old log overwritten: same_path=%t content=%q", first == second, data)
	}
}

func TestJobLogCreationPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.log")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if writer := newCappedFileWriter(path); writer != nil {
		writer.Close()
		t.Fatal("opened an existing job log for writing")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("existing log changed: %q, %v", data, err)
	}
}
