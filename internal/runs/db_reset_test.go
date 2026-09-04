package runs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenResetNoticeSaysDatabaseWasDiscarded(t *testing.T) {
	root := t.TempDir()
	buildV1DB(t, filepath.Join(root, DBFile))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = w
	st, openErr := Open(root)
	os.Stderr = original
	_ = w.Close()
	captured, _ := io.ReadAll(r)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer st.Close()
	line := string(captured)
	if !strings.Contains(line, "discarding and resetting") {
		t.Fatalf("reset notice = %q", line)
	}
	if strings.Contains(line, "preserved") || strings.Contains(line, ".old-") {
		t.Fatalf("reset notice promises compatibility backup: %q", line)
	}
}

func TestRemoveDatabaseFilesScopeIsExact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(candidate, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(root, "not-the-database.txt")
	if err := os.WriteFile(unrelated, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeDatabaseFiles(path); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("database file %q remains: %v", candidate, err)
		}
	}
	got, err := os.ReadFile(unrelated)
	if err != nil || string(got) != "leave me alone" {
		t.Fatalf("unrelated sibling content=%q err=%v", got, err)
	}
}
