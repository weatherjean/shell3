//go:build unix

package mediadir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepRemovesOldKeepsNew(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "sent-1-old.txt")
	fresh := filepath.Join(dir, "img-new.png")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	removed, err := Sweep(dir, 24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old file survived")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh file removed")
	}
}

func TestSweepZeroKeepsForever(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sent-1-a.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(p, now.Add(-9000*time.Hour), now.Add(-9000*time.Hour)); err != nil {
		t.Fatal(err)
	}
	removed, err := Sweep(dir, 0, now)
	if err != nil || removed != 0 {
		t.Fatalf("Sweep(keep=0) = %d, %v; want 0, nil", removed, err)
	}
}

func TestSweepSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "target.txt")
	if err := os.WriteFile(target, []byte("target bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "img-old-link.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(target, now.Add(-9000*time.Hour), now.Add(-9000*time.Hour)); err != nil {
		t.Fatal(err)
	}

	removed, err := Sweep(dir, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 {
		t.Fatalf("Sweep removed = %d, want 0 (symlinks must never be swept)", removed)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink itself was removed: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}

func TestSweepSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a-subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(sub, "old-inner-file.txt")
	if err := os.WriteFile(inner, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-9000 * time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(inner, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := Sweep(dir, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 {
		t.Fatalf("Sweep removed = %d, want 0 (subdirectories must never be swept)", removed)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("subdirectory was removed: %v", err)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("file inside subdirectory was removed: %v", err)
	}
}

func TestSweepCutoffIsStrictlyBefore(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	keep := 24 * time.Hour
	cutoff := now.Add(-keep)

	atCutoff := filepath.Join(dir, "img-at-cutoff.png")
	if err := os.WriteFile(atCutoff, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(atCutoff, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}

	justPast := filepath.Join(dir, "img-just-past-cutoff.png")
	if err := os.WriteFile(justPast, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(justPast, cutoff.Add(-time.Second), cutoff.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	removed, err := Sweep(dir, keep, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Sweep removed = %d, want 1 (only the file strictly older than cutoff)", removed)
	}
	if _, err := os.Stat(atCutoff); err != nil {
		t.Fatalf("file exactly at cutoff was removed, want kept: %v", err)
	}
	if _, err := os.Stat(justPast); !os.IsNotExist(err) {
		t.Fatalf("file older than cutoff survived: err=%v", err)
	}
}

func TestSweepHandlesHostileFilenames(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-9000 * time.Hour)

	hostile := []string{
		"sent-with spaces.txt",
		"img-é漢字.png",
		"-leading-dash.txt",
		"trailing-dot.txt.",
	}
	for _, name := range hostile {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("chtimes %q: %v", name, err)
		}
	}

	removed, err := Sweep(dir, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != len(hostile) {
		t.Fatalf("Sweep removed = %d, want %d", removed, len(hostile))
	}
	for _, name := range hostile {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("hostile-named file %q survived: err=%v", name, err)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("parent directory itself was affected: %v", err)
	}
}

func TestSweepMissingDirReturnsZeroNil(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	removed, err := Sweep(dir, 24*time.Hour, time.Now())
	if removed != 0 || err != nil {
		t.Fatalf("Sweep(missing dir) = %d, %v; want 0, nil", removed, err)
	}
}
