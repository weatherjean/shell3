package fsstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteConcurrentPublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	errCh := make(chan error, 8)
	for range 8 {
		go func() { errCh <- Write(path, []byte(strings.Repeat("x", 4096))) }()
	}
	for range 8 {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != strings.Repeat("x", 4096) {
		t.Fatalf("size=%d error=%v", len(data), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v %v", entries, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}

func TestWriteFailureLeavesDestinationAndCleansTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new")); err == nil {
		t.Fatal("replaced a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("destination changed: %v %v", entries, err)
	}
}
