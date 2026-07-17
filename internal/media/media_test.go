//go:build unix

package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirEnvOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", want)
	got, err := Dir()
	if err != nil || got != want {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
	}
}

func TestDirDefaultUnderHome(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, ".shell3", "media") {
		t.Fatalf("Dir() = %q, want ~/.shell3/media", got)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Fatalf("Dir() must create the directory: %v", err)
	}
}
