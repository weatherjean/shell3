package modelproxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
)

// waitForFile polls until path exists and is non-empty, or fails after timeout.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func TestEnsureRunsCommandOncePerName(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")

	s := New(dir, applog.Noop{})
	cmd := fmt.Sprintf("echo run >> %q", marker)
	s.Ensure("m", cmd)
	s.Ensure("m", cmd) // guarded: must not spawn a second time

	out := waitForFile(t, marker)
	// Give any erroneous second spawn a chance to also write before asserting.
	time.Sleep(150 * time.Millisecond)
	out = mustRead(t, marker)
	if got := strings.Count(out, "run"); got != 1 {
		t.Fatalf("expected command to run exactly once, ran %d times:\n%s", got, out)
	}
}

func TestEnsureEmptyCommandIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, applog.Noop{})
	s.Ensure("m", "")
	if _, err := os.Stat(filepath.Join(dir, "proxy-m.log")); !os.IsNotExist(err) {
		t.Fatalf("empty command must not create a log file")
	}
}

func TestEnsureRedirectsOutputToLog(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, applog.Noop{})
	s.Ensure("main", "echo hello-from-proxy")

	out := waitForFile(t, filepath.Join(dir, "proxy-main.log"))
	if !strings.Contains(out, "hello-from-proxy") {
		t.Fatalf("proxy stdout not captured in log:\n%s", out)
	}
}

func TestEnsureFailingCommandDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, applog.Noop{})
	// Command exits non-zero; spawn is fire-and-forget so this must be harmless.
	s.Ensure("m", "exit 7")
	// Still guarded after a soft failure.
	s.Ensure("m", "exit 7")
}

func TestEnsureSanitizesNameForLogPath(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, applog.Noop{})
	s.Ensure("vendor/model:v1", "echo x")
	out := waitForFile(t, filepath.Join(dir, "proxy-vendor_model_v1.log"))
	if !strings.Contains(out, "x") {
		t.Fatalf("sanitized log path not used:\n%s", out)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
