//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

func TestBootWritesOneCompleteKitAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lisp")
	cmd := newBootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := lispconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main == nil || len(cfg.Skills) != 10 || cfg.Skills[0].Name != "sd-file-editing" || cfg.Skills[5].Name != "shell3-inbox" || cfg.Memory != "" {
		t.Fatalf("generated kit = main:%+v skills:%d memory:%q", cfg.Main, len(cfg.Skills), cfg.Memory)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "shell3.lisp" {
		t.Fatalf("boot files = %+v err=%v", entries, err)
	}
	cmd = newBootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{path})
	if err := cmd.Execute(); err == nil {
		t.Fatal("boot overwrote an existing kit")
	}
}

func TestBootDefaultsToShell3Home(t *testing.T) {
	home := t.TempDir()
	original := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = original })
	cmd := newBootCommand()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".shell3", "shell3.lisp")); err != nil {
		t.Fatalf("default kit: %v", err)
	}
}
