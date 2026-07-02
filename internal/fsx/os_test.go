package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOSReadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	fs := OS{}
	if err := fs.WriteTextFile(context.Background(), p, "hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := fs.ReadTextFile(context.Background(), p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("got %q", got)
	}
}

func TestOSReadMissingIsErrNotExist(t *testing.T) {
	fs := OS{}
	_, err := fs.ReadTextFile(context.Background(), filepath.Join(t.TempDir(), "nope.txt"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

func TestOSReadDirIsErrIsDir(t *testing.T) {
	fs := OS{}
	_, err := fs.ReadTextFile(context.Background(), t.TempDir())
	if !errors.Is(err, ErrIsDir) {
		t.Fatalf("want ErrIsDir, got %v", err)
	}
}

func TestOSWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub/deep/a.txt")
	fs := OS{}
	if err := fs.WriteTextFile(context.Background(), p, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestOSWritePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs := OS{}
	if err := fs.WriteTextFile(context.Background(), p, "new"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode lost: got %o want 0755", info.Mode().Perm())
	}
}

func TestOSWriteNewFileDefaultMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.txt")
	fs := OS{}
	if err := fs.WriteTextFile(context.Background(), p, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("default mode wrong: got %o want 0644", info.Mode().Perm())
	}
}
