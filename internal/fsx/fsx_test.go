package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := WriteTextFile(context.Background(), p, "hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadTextFile(context.Background(), p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("got %q", got)
	}
}

func TestReadMissingIsErrNotExist(t *testing.T) {
	_, err := ReadTextFile(context.Background(), filepath.Join(t.TempDir(), "nope.txt"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

func TestReadDirIsErrIsDir(t *testing.T) {
	_, err := ReadTextFile(context.Background(), t.TempDir())
	if !errors.Is(err, ErrIsDir) {
		t.Fatalf("want ErrIsDir, got %v", err)
	}
}

func TestWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub/deep/a.txt")
	if err := WriteTextFile(context.Background(), p, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWritePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteTextFile(context.Background(), p, "new"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode lost: got %o want 0755", info.Mode().Perm())
	}
}

func TestWriteNewFileDefaultMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.txt")
	if err := WriteTextFile(context.Background(), p, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("default mode wrong: got %o want 0644", info.Mode().Perm())
	}
}
