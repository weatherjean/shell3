//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func pathTestCommand(config, workDir *string, here *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	addRuntimeFlags(cmd, config, workDir, here)
	return cmd
}

func TestResolveRuntimePathsDefaultsToShell3Home(t *testing.T) {
	home := t.TempDir()
	original := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = original })
	var config, workDir string
	var here bool
	cmd := pathTestCommand(&config, &workDir, &here)
	got, err := resolveRuntimePaths(cmd, config, workDir, here)
	if err != nil {
		t.Fatal(err)
	}
	if got.config != filepath.Join(home, ".shell3", "shell3.lisp") ||
		got.workDir != filepath.Join(home, ".shell3", "workdir") {
		t.Fatalf("paths = %+v", got)
	}
}

func TestResolveRuntimePathsHereAndExplicitModes(t *testing.T) {
	var config, workDir string
	var here bool
	cmd := pathTestCommand(&config, &workDir, &here)
	if err := cmd.Flags().Set("here", "true"); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRuntimePaths(cmd, config, workDir, here)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got.config) != "shell3.lisp" || got.workDir != filepath.Dir(got.config) {
		t.Fatalf("--here paths = %+v", got)
	}

	config, workDir, here = "", "", false
	cmd = pathTestCommand(&config, &workDir, &here)
	if err := cmd.Flags().Set("config", "kit.lisp"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntimePaths(cmd, config, workDir, here); err == nil ||
		!strings.Contains(err.Error(), "must be provided together") {
		t.Fatalf("single explicit path error = %v", err)
	}
	if err := cmd.Flags().Set("workdir", "."); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("here", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntimePaths(cmd, config, workDir, here); err == nil ||
		!strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mixed mode error = %v", err)
	}
}
