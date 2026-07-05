//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/runs"
)

// seedSession creates a session with the given configPath in a runs store rooted at
// projectRoot and returns the session ID.
func seedSession(t *testing.T, projectRoot, workdir, configPath string) string {
	t.Helper()
	st, err := runs.Open(projectRoot)
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	id, err := st.NewSession(runs.Meta{
		Workdir:    workdir,
		ConfigPath: configPath,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return id
}

// TestResolveResumeConfig covers the precedence rules: explicit --config wins,
// resumeID=="" short-circuits, otherwise the resumed session's recorded
// config_path is used (or "" when none was recorded).
func TestResolveResumeConfig(t *testing.T) {
	// Use a temp dir as cwd so paths.NewLocal resolves inside it.
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, ".shell3_project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	seededID := seedSession(t, projectRoot, "/work/seeded", "/seeded/.shell3/shell3.lua")
	noCfgID := seedSession(t, projectRoot, "/work/nocfg", "")

	// Point os.Getwd() to tmpDir.
	t.Chdir(tmpDir)

	t.Run("recorded config used", func(t *testing.T) {
		got, err := resolveResumeConfig(seededID, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/seeded/.shell3/shell3.lua" {
			t.Errorf("got %q, want /seeded/.shell3/shell3.lua", got)
		}
	})

	t.Run("explicit flag overrides", func(t *testing.T) {
		got, err := resolveResumeConfig(seededID, "/explicit.lua")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/explicit.lua" {
			t.Errorf("got %q, want /explicit.lua", got)
		}
	})

	t.Run("no resume short-circuits", func(t *testing.T) {
		got, err := resolveResumeConfig("", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no recorded config returns empty", func(t *testing.T) {
		got, err := resolveResumeConfig(noCfgID, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("unknown session id returns empty", func(t *testing.T) {
		got, err := resolveResumeConfig("20991231T235959.000000000", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("got %q, want empty for unknown session", got)
		}
	})

}
