package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/sink"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// The success/agent_done path of RunOnce is covered end-to-end by
// test/cli_e2e_test.go (TestCLIE2E_AppendSinkfileSelfReport). These tests pin
// the once.go logic that path does not exercise: preview clamping and the
// error-status self-report branch.

func TestTruncatePreview(t *testing.T) {
	// Under the cap: returned verbatim, no ellipsis.
	if got := truncatePreview("hello"); got != "hello" {
		t.Errorf("short string altered: %q", got)
	}
	// Exactly at the cap (byte length == previewMax): still verbatim.
	atCap := strings.Repeat("a", previewMax)
	if got := truncatePreview(atCap); got != atCap {
		t.Errorf("at-cap string altered: len=%d want %d", len(got), previewMax)
	}
	// Over the cap with 3-byte runes positioned so previewMax lands mid-rune:
	// the clamp must back up to a rune start (no split rune) and append "…".
	long := strings.Repeat("世", previewMax) // 3 bytes each → 3*previewMax bytes
	got := truncatePreview(long)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated preview is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("over-cap preview missing ellipsis: %q", got)
	}
	// previewMax=200 is not a multiple of 3, so the naive cut would split a
	// rune; the backup drops it, leaving 66 whole runes plus the ellipsis.
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n != previewMax/3 {
		t.Errorf("kept %d runes, want %d (clamped to rune boundary)", n, previewMax/3)
	}
}

func TestSelfReport_AppendsAgentDoneWithErrorStatus(t *testing.T) {
	sinkPath := filepath.Join(t.TempDir(), "sink.jsonl")
	spec := shell3.Spec{
		AppendSinkFile: sinkPath,
		ID:             "job-1",
		OutPath:        "/transcripts/job-1.jsonl",
	}

	selfReport(spec, "boom", true) // errored run

	n := readOnlyNotification(t, sinkPath)
	if n.Kind != sink.KindAgentDone {
		t.Errorf("kind = %q, want %q", n.Kind, sink.KindAgentDone)
	}
	if n.Status != "error" {
		t.Errorf("status = %q, want error", n.Status)
	}
	if n.ID != "job-1" {
		t.Errorf("id = %q, want job-1", n.ID)
	}
	if n.Transcript != spec.OutPath {
		t.Errorf("transcript = %q, want spec.OutPath %q", n.Transcript, spec.OutPath)
	}
	if n.Preview != "boom" {
		t.Errorf("preview = %q, want boom", n.Preview)
	}
}

// readOnlyNotification reads sinkPath and asserts it holds exactly one JSONL
// notification, returning it decoded.
func readOnlyNotification(t *testing.T, sinkPath string) sink.Notification {
	t.Helper()
	data, err := os.ReadFile(sinkPath)
	if err != nil {
		t.Fatalf("sink not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one notification, got %d:\n%s", len(lines), data)
	}
	var n sink.Notification
	if err := json.Unmarshal([]byte(lines[0]), &n); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	return n
}
