//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
)

func TestJobsCommand_FlagsPresent(t *testing.T) {
	cmd := newJobsCommand()
	fs := cmd.Flags()
	for _, name := range []string{"config", "workdir", "page", "page-size"} {
		if fs.Lookup(name) == nil {
			t.Errorf("jobs is missing --%s", name)
		}
	}
	if cmd.Use == "" {
		t.Error("jobs Use is empty")
	}
}

// TestJobsCommand_RunE seeds a live-pid job at the canonical DB under a temp
// HOME and asserts the command lists it when scoped via --workdir.
func TestJobsCommand_RunE(t *testing.T) {
	tmpHome := t.TempDir()
	dbPath := paths.NewGlobal(tmpHome).DB
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	wd := "/work/jobs-test"
	if err := st.AddJob(store.Job{
		ID:      "bg_x",
		PID:     os.Getpid(), // live pid so prune keeps it
		Cmd:     "sleep 1",
		Log:     "/tmp/x.log",
		Workdir: wd,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)

	cmd := newJobsCommand()
	cmd.SetArgs([]string{"--workdir", wd})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("jobs execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "bg_x") {
		t.Errorf("expected job bg_x in output, got: %q", out)
	}
}
