//go:build unix

package main

import (
	"fmt"

	"github.com/weatherjean/shell3/internal/store"
)

// resolveResumeConfig returns the config path to run a resume under: the explicit
// --config flag if set, else the resumed session's recorded config_path, else ""
// (falling back to default resolution downstream). resumeID==0 → returns flagConfig.
func resolveResumeConfig(resumeID int64, flagConfig string) (string, error) {
	if flagConfig != "" || resumeID == 0 {
		return flagConfig, nil
	}
	dbPath, err := canonicalDBPath("")
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: %w", err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: open store: %w", err)
	}
	defer func() { _ = st.Close() }()
	cp, err := st.SessionConfigPath(resumeID)
	if err != nil {
		return "", fmt.Errorf("run: resolve resume config: %w", err)
	}
	return cp, nil
}
