package runs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen_UnreadableFilePreservedOnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not block reads")
	}
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	if err := os.WriteFile(path, []byte("some bytes that would otherwise be a valid-ish file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := Open(root); err == nil {
		t.Fatal("Open on an unreadable file: want error, got nil")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file vanished after failed Open: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != DBFile {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("root dir after refused Open = %v, want only %q (no aside file created)", names, DBFile)
	}
}

func TestOpen_CorruptFilePreservedOnError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	if err := os.WriteFile(path, []byte("this is not a sqlite database, just garbage bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil {
		t.Fatal("Open on a corrupt file: want error, got nil")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file vanished after failed Open: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("corrupt file was modified/replaced by a failed Open")
	}
}
