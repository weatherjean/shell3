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
		WrapBash: func(_ context.Context, cmd string) (string, bool, string, error) {
			return cmd, false, "denied by policy", nil
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
		WrapBash: func(_ context.Context, _ string) (string, bool, string, error) {
			return "touch " + rewritten, true, "", nil
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
