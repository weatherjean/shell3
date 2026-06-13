//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
)

// TestResolveResumeConfig covers the precedence rules: explicit --config wins,
// resumeID==0 short-circuits, otherwise the resumed session's recorded
// config_path is used (or "" when none was recorded).
func TestResolveResumeConfig(t *testing.T) {
	tmpHome := t.TempDir()
	dbDir := filepath.Join(tmpHome, ".shell3", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.NewGlobal(tmpHome).DB)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := st.StartSession("proj-uuid", "/work/seeded", "/seeded/.shell3/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	noCfg, err := st.StartSession("proj-uuid", "/work/nocfg", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)

	t.Run("recorded config used", func(t *testing.T) {
		got, err := resolveResumeConfig(seeded, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/seeded/.shell3/shell3.lua" {
			t.Errorf("got %q, want /seeded/.shell3/shell3.lua", got)
		}
	})

	t.Run("explicit flag overrides", func(t *testing.T) {
		got, err := resolveResumeConfig(seeded, "/explicit.lua")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/explicit.lua" {
			t.Errorf("got %q, want /explicit.lua", got)
		}
	})

	t.Run("no resume short-circuits", func(t *testing.T) {
		got, err := resolveResumeConfig(0, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no recorded config returns empty", func(t *testing.T) {
		got, err := resolveResumeConfig(noCfg, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
