package applog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoggerWritesPrivateJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "errors.jsonl")
	lg, closer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lg.Error("turn failed", os.ErrNotExist, "session", "s1")
	lg.Warn("retry failed", "cause", os.ErrPermission)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("records = %d, want 2: %q", len(lines), data)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("invalid JSONL record: %v: %q", err, lines[0])
	}
	if record["level"] != "error" || record["message"] != "turn failed" || !strings.Contains(record["error"].(string), "does not exist") {
		t.Fatalf("record = %#v", record)
	}
	if got := record["fields"].(map[string]any)["session"]; got != "s1" {
		t.Fatalf("session = %#v", got)
	}
	if err := json.Unmarshal([]byte(lines[1]), &record); err != nil {
		t.Fatalf("invalid JSONL record: %v: %q", err, lines[1])
	}
	if got := record["fields"].(map[string]any)["cause"]; !strings.Contains(got.(string), "permission denied") {
		t.Fatalf("cause = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestFileLoggerRotatesDuringWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.jsonl")
	l := &fileLogger{path: path, maxBytes: 128, archives: 2}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	l.lock = lock
	if err := l.open(); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 20; i++ {
		l.Info("a record long enough to force rotation", "index", i)
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected archive .3: %v", err)
	}
}

func TestTwoLoggersFollowCrossProcessRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.jsonl")
	open := func() *fileLogger {
		l := &fileLogger{path: path, maxBytes: 180, archives: 2}
		lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		l.lock = lock
		if err := l.open(); err != nil {
			t.Fatal(err)
		}
		return l
	}
	first, second := open(), open()
	defer first.Close()
	defer second.Close()
	for i := 0; i < 20; i++ {
		first.Info("first writer", "index", i)
		second.Info("second writer", "index", i)
	}
	for _, name := range []string{path, path + ".1"} {
		data, err := os.ReadFile(name)
		if err != nil || len(data) == 0 {
			t.Fatalf("log %s empty or missing: %q, %v", name, data, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("invalid JSONL after concurrent rotation: %q: %v", line, err)
			}
		}
	}
}

func TestOpenProtectsExistingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.jsonl")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, closer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
