//go:build unix

package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBashHandler_WrapBashBlocksExecution proves a blocking WrapBash prevents
// the command from ever running: the handler returns the block message and the
// command's side effect (creating a file) never happens.
func TestBashHandler_WrapBashBlocksExecution(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	cfg := ToolConfig{
		WorkDir: dir,
		WrapBash: func(_ context.Context, cmd string) ([]string, bool, string, error) {
			return nil, false, "denied by policy", nil
		},
	}
	args := json.RawMessage(`{"command":"touch ` + marker + `"}`)
	out, err := BashHandler{}.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "blocked by wrap_bash") || !strings.Contains(out, "denied by policy") {
		t.Fatalf("expected a wrap_bash block message, got %q", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("blocked command still executed (marker file was created)")
	}
}

// TestBashHandler_WrapBashRewritesCommand proves a rewriting WrapBash changes
// the command that actually runs: the hook swaps the command for one that
// creates a DIFFERENT marker, and only that marker appears.
func TestBashHandler_WrapBashRewritesCommand(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	rewritten := filepath.Join(dir, "rewritten")
	cfg := ToolConfig{
		WorkDir: dir,
		WrapBash: func(_ context.Context, _ string) ([]string, bool, string, error) {
			return []string{"bash", "-c", "touch " + rewritten}, true, "", nil
		},
	}
	args := json.RawMessage(`{"command":"touch ` + original + `"}`)
	if _, err := (BashHandler{}).Execute(context.Background(), "1", args, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rewritten); err != nil {
		t.Fatalf("rewritten command did not run: %v", err)
	}
	if _, err := os.Stat(original); err == nil {
		t.Fatal("original command ran despite the rewrite")
	}
}

// TestBashHandler_WrapBashSwapsRunner proves an argv table is exec'd
// positionally: a shell-metachar payload passed as $1 lands as a single argv
// element and is printed verbatim, NOT re-parsed by an outer bash -c. If the
// runner argv were re-joined and re-parsed, the `; touch <marker>` in the
// payload would execute as a separate command and create the marker file.
func TestBashHandler_WrapBashSwapsRunner(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	payload := `a b "c"; touch ` + marker
	cfg := ToolConfig{
		WorkDir: dir,
		WrapBash: func(_ context.Context, _ string) ([]string, bool, string, error) {
			// $0="_", $1=payload — printf echoes $1 with no re-parsing.
			return []string{"bash", "-c", `printf '%s' "$1"`, "_", payload}, true, "", nil
		},
	}
	out, err := BashHandler{}.Execute(context.Background(), "id", json.RawMessage(`{"command":"ignored"}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out != payload {
		t.Fatalf("argv not passed through verbatim: got %q want %q", out, payload)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("payload metachars were re-parsed and executed (marker created): %q", out)
	}
}
