//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestReadSessionCommand_FlagsPresent(t *testing.T) {
	cmd := newReadSessionCommand()
	fs := cmd.Flags()
	for _, name := range []string{"config", "page", "page-size"} {
		if fs.Lookup(name) == nil {
			t.Errorf("read-session is missing --%s", name)
		}
	}
	if cmd.Use == "" {
		t.Error("read-session Use is empty")
	}
	// Exactly one positional arg.
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("read-session should reject zero args")
	}
	if err := cmd.Args(cmd, []string{"1", "2"}); err == nil {
		t.Error("read-session should reject two args")
	}
	if err := cmd.Args(cmd, []string{"1"}); err != nil {
		t.Errorf("read-session should accept exactly one arg: %v", err)
	}
}

// TestReadSessionCommand_RunE seeds a temp DB at the canonical path, seeds a
// session with two turns, and asserts the transcript prints both turns in
// chronological order (user before assistant).
func TestReadSessionCommand_RunE(t *testing.T) {
	tmpHome := t.TempDir()
	dbDir := filepath.Join(tmpHome, ".shell3", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession("proj-alpha", "/work/alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendHistory(id, "user", "hello world"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendHistory(id, "assistant", "hi there"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)

	cmd := newReadSessionCommand()
	cmd.SetArgs([]string{strconv.FormatInt(id, 10)})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("read-session execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	iHello := strings.Index(out, "hello world")
	iHi := strings.Index(out, "hi there")
	if iHello < 0 {
		t.Errorf("expected 'hello world' in output, got: %q", out)
	}
	if iHi < 0 {
		t.Errorf("expected 'hi there' in output, got: %q", out)
	}
	if iHello >= 0 && iHi >= 0 && iHello > iHi {
		t.Errorf("turns out of order: user turn must precede assistant turn, got: %q", out)
	}
}

// TestReadSessionCommand_Pagination proves --page/--page-size genuinely slice:
// a page-size of 1 shows exactly one turn, and --page advances through turns.
func TestReadSessionCommand_Pagination(t *testing.T) {
	tmpHome := t.TempDir()
	dbDir := filepath.Join(tmpHome, ".shell3", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession("proj-alpha", "/work/alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ role, content string }{
		{"user", "turn-one-content"},
		{"assistant", "turn-two-content"},
		{"user", "turn-three-content"},
	} {
		if err := st.AppendHistory(id, c.role, c.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	run := func(args ...string) string {
		cmd := newReadSessionCommand()
		cmd.SetArgs(args)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("read-session execute: %v\noutput: %s", err, buf.String())
		}
		return buf.String()
	}

	sid := strconv.FormatInt(id, 10)

	page0 := run(sid, "--page-size", "1", "--page", "0")
	if !strings.Contains(page0, "turn-one-content") {
		t.Errorf("page 0 should contain first turn, got: %q", page0)
	}
	if strings.Contains(page0, "turn-two-content") || strings.Contains(page0, "turn-three-content") {
		t.Errorf("page 0 (size 1) leaked later turns, got: %q", page0)
	}

	page1 := run(sid, "--page-size", "1", "--page", "1")
	if !strings.Contains(page1, "turn-two-content") {
		t.Errorf("page 1 should contain second turn, got: %q", page1)
	}
	if strings.Contains(page1, "turn-one-content") || strings.Contains(page1, "turn-three-content") {
		t.Errorf("page 1 (size 1) leaked other turns, got: %q", page1)
	}
}
