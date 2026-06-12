//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestListProjectsCommand_FlagsPresent(t *testing.T) {
	cmd := newListProjectsCommand()
	fs := cmd.Flags()
	for _, name := range []string{"page", "page-size"} {
		if fs.Lookup(name) == nil {
			t.Errorf("list-projects is missing --%s", name)
		}
	}
	if cmd.Use == "" {
		t.Error("list-projects Use is empty")
	}
}

// TestListProjectsCommand_RunE seeds a temp DB and invokes the command,
// asserting that the seeded project UUID appears in the output.
func TestListProjectsCommand_RunE(t *testing.T) {
	// Build a temp home with the DB at the canonical path.
	tmpHome := t.TempDir()
	dbDir := filepath.Join(tmpHome, ".shell3", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "shell3.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession("proj-alpha", "/work/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession("proj-alpha", "/work/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession("proj-beta", "/work/beta"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Override HOME so paths.NewGlobal resolves to our temp dir.
	t.Setenv("HOME", tmpHome)

	cmd := newListProjectsCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list-projects execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "proj-alpha") {
		t.Errorf("expected 'proj-alpha' in output, got: %q", out)
	}
	if !strings.Contains(out, "proj-beta") {
		t.Errorf("expected 'proj-beta' in output, got: %q", out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("expected '2 sessions' for proj-alpha in output, got: %q", out)
	}
}
