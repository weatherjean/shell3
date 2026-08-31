//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mediaTree(t *testing.T) (old, fresh string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", dir)
	old = filepath.Join(dir, "img-old.png")
	fresh = filepath.Join(dir, "img-fresh.png")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	return old, fresh
}

func TestRunJanitorsSweepsMediaPastTheCutoff(t *testing.T) {
	old, fresh := mediaTree(t)
	var out strings.Builder
	runJanitors(t.TempDir(), 0, 7, &out)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a file 8 days old should be swept at media_keep_days=7 (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh file must survive: %v", err)
	}
	if !strings.Contains(out.String(), "removed 1 media files") {
		t.Errorf("sweep should report what it deleted; got %q", out.String())
	}
}

func TestRunJanitorsKeepsMediaForeverByDefault(t *testing.T) {
	old, fresh := mediaTree(t)
	var out strings.Builder
	runJanitors(t.TempDir(), 0, 0, &out)

	for _, p := range []string{old, fresh} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("media_keep_days=0 must keep %s: %v", filepath.Base(p), err)
		}
	}
	if strings.Contains(out.String(), "media") {
		t.Errorf("no media sweep should be reported; got %q", out.String())
	}
}

func TestRunJanitorsFailsOpen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", filepath.Join(dir, "file-not-a-dir"))
	if err := os.WriteFile(filepath.Join(dir, "file-not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runJanitors(filepath.Join(dir, "nonexistent"), 30, 7, &out)
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("a janitor fault should warn; got %q", out.String())
	}
}
