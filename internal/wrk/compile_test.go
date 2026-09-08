package wrk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileLauncherQuotesPathsAndAcceptsWaits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project's files")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "shell3.lisp")
	workflow := filepath.Join(dir, "task.wrk.lisp")
	for name, body := range map[string]string{config: "(shell3 (version 1))", workflow: `(task "demo" (wait approval (for (event "go"))))`} {
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script, err := Compile(&Definition{Path: workflow}, config)
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "capture")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", launcher, "request with spaces")
	cmd.Env = append(os.Environ(), "SHELL3_BIN="+fake)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{config + "\n", workflow + "\n", "request with spaces\n", "--config-sha256\n", "--workflow-sha256\n"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}
