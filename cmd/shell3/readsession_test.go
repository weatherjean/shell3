//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// makeRunsStore sets up a runs store in a temp dir and changes the working
// directory to the temp dir (restoring it on cleanup). Returns the store and
// the project root path.
func makeRunsStore(t *testing.T) (*runs.Store, string) {
	t.Helper()
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, ".shell3_project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := runs.Open(projectRoot)
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	t.Chdir(tmpDir)
	return st, projectRoot
}

func TestReadSessionCommand_ArgsRequired(t *testing.T) {
	cmd := newReadSessionCommand()
	if cmd.Use == "" {
		t.Error("read-session Use is empty")
	}
	// Exactly one positional arg.
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("read-session should reject zero args")
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("read-session should reject two args")
	}
	if err := cmd.Args(cmd, []string{"some-id"}); err != nil {
		t.Errorf("read-session should accept exactly one arg: %v", err)
	}
}

// TestReadSessionCommand_RunE seeds a temp runs store, writes two messages,
// and asserts the transcript prints both in chronological order (user first,
// then assistant).
func TestReadSessionCommand_RunE(t *testing.T) {
	st, _ := makeRunsStore(t)

	id, err := st.NewSession(runs.Meta{Workdir: "/work/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: "user", Content: "hello world"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: "assistant", Content: "hi there"}); err != nil {
		t.Fatal(err)
	}

	cmd := newReadSessionCommand()
	cmd.SetArgs([]string{id})
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

// TestReadSessionCommand_AllMessages verifies all messages are printed without
// pagination (the new implementation has no --page flag).
func TestReadSessionCommand_AllMessages(t *testing.T) {
	st, _ := makeRunsStore(t)

	id, err := st.NewSession(runs.Meta{Workdir: "/work/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	contents := []struct{ role, content string }{
		{"user", "turn-one-content"},
		{"assistant", "turn-two-content"},
		{"user", "turn-three-content"},
	}
	for _, c := range contents {
		if err := st.AppendMessage(id, llm.Message{Role: llm.Role(c.role), Content: c.content}); err != nil {
			t.Fatal(err)
		}
	}

	cmd := newReadSessionCommand()
	cmd.SetArgs([]string{id})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("read-session execute: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()

	for _, c := range contents {
		if !strings.Contains(out, c.content) {
			t.Errorf("expected %q in output, got: %q", c.content, out)
		}
	}

	// Verify chronological order.
	i1 := strings.Index(out, "turn-one-content")
	i2 := strings.Index(out, "turn-two-content")
	i3 := strings.Index(out, "turn-three-content")
	if i1 > i2 || i2 > i3 {
		t.Errorf("turns out of order in output: %q", out)
	}

	// Verify roles are printed.
	if !strings.Contains(out, "user") {
		t.Errorf("expected 'user' role in output, got: %q", out)
	}
	if !strings.Contains(out, "assistant") {
		t.Errorf("expected 'assistant' role in output, got: %q", out)
	}
}

// TestReadSessionCommand_Empty verifies no error (and empty output) for a
// session with no messages.
func TestReadSessionCommand_Empty(t *testing.T) {
	st, _ := makeRunsStore(t)

	id, err := st.NewSession(runs.Meta{Workdir: "/work/alpha"})
	if err != nil {
		t.Fatal(err)
	}

	cmd := newReadSessionCommand()
	cmd.SetArgs([]string{id})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("read-session execute for empty session: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty session, got: %q", buf.String())
	}
}

func TestReadSessionCommand_NotFound(t *testing.T) {
	_, _ = makeRunsStore(t)

	cmd := newReadSessionCommand()
	cmd.SetArgs([]string{"20991231T235959.000000000"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// The command deliberately distinguishes a missing session from an empty
	// one (LoadMessages returns nil,nil for a missing file) — pin the error.
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("want a 'no session' error for an unknown id, got %v", err)
	}
}
