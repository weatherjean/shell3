//go:build unix

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestListSessionsCommand_FlagsPresent(t *testing.T) {
	cmd := newListSessionsCommand()
	fs := cmd.Flags()
	for _, name := range []string{"project-id", "page", "page-size"} {
		if fs.Lookup(name) == nil {
			t.Errorf("list-sessions is missing --%s", name)
		}
	}
	if cmd.Use == "" {
		t.Error("list-sessions Use is empty")
	}
}

// TestListSessionsCommand_RunE seeds a temp DB at the canonical path and invokes
// the command, asserting session ids appear and --project-id scopes the output.
func TestListSessionsCommand_RunE(t *testing.T) {
	tmpHome := t.TempDir()
	dbDir := filepath.Join(tmpHome, ".shell3", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.StartSession("proj-alpha", "/work/alpha", "/work/alpha/.shell3/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	st.AppendHistory(a, "user", "alpha question")
	if _, err := st.StartSession("proj-beta", "/work/beta", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)

	// Scoped to proj-alpha: only session a, with its preview.
	cmd := newListSessionsCommand()
	cmd.SetArgs([]string{"--project-id", "proj-alpha"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list-sessions execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, fmt.Sprintf("%d\t", a)) {
		t.Errorf("expected session %d in scoped output, got: %q", a, out)
	}
	if !strings.Contains(out, "alpha question") {
		t.Errorf("expected preview 'alpha question' in output, got: %q", out)
	}
	if !strings.Contains(out, "cfg:/work/alpha/.shell3/shell3.lua") {
		t.Errorf("expected config path in output, got: %q", out)
	}
	if strings.Contains(out, "/work/beta") || strings.Count(out, "\n") != 1 {
		t.Errorf("project scope leaked other-project sessions: %q", out)
	}
}
