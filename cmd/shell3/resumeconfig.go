//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
)

// resolveResumeConfig returns the config path to run a resume under: the
// explicit --config flag if set, else the resumed session's recorded
// config_path (from its meta.json via runs.Store), else "" (falling back to
// default resolution downstream). resumeID=="" → returns flagConfig unchanged.
func resolveResumeConfig(resumeID string, flagConfig string) (string, error) {
	if flagConfig != "" || resumeID == "" {
		return flagConfig, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: %w", err)
	}
	local := paths.NewLocal(cwd)
	st, err := runs.Open(local.Root)
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: open runs: %w", err)
	}
	metas, err := st.ListSessions(0)
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: list sessions: %w", err)
	}
	for _, m := range metas {
		if m.ID == resumeID {
			return m.ConfigPath, nil
		}
	}
	// Session not found — return "" so downstream falls back to default config.
	return "", nil
}
