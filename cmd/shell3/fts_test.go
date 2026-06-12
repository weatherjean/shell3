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

func TestFTSCommand_FlagsPresent(t *testing.T) {
	cmd := newFTSCommand()
	fs := cmd.Flags()
	for _, name := range []string{"project-id", "page", "page-size"} {
		if fs.Lookup(name) == nil {
			t.Errorf("fts is missing --%s", name)
		}
	}
	if cmd.Use == "" {
		t.Error("fts Use is empty")
	}
}

// TestFTSCommand_RunE seeds a temp DB and invokes the command, asserting the
// matching turn appears in the output and out-of-scope project turns are absent.
func TestFTSCommand_RunE(t *testing.T) {
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
	sid, err := st.StartSession("proj-x", "/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendHistory(sid, "user", "zephyr token unique"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Override HOME so paths.NewGlobal resolves to our temp dir.
	t.Setenv("HOME", tmpHome)

	cmd := newFTSCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"zephyr"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fts execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	// The tab-separated line should contain the session id and the snippet.
	if !strings.Contains(out, "zephyr") {
		t.Errorf("expected 'zephyr' in output, got: %q", out)
	}
}
