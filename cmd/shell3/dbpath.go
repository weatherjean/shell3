//go:build unix

package main

import (
	"os"

	"github.com/weatherjean/shell3/internal/paths"
)

// canonicalDBPath resolves the single canonical DB. It is always
// ~/.shell3/data/shell3.db, matching the runtime, which writes history there
// (see agentsetup.resolvePaths, which builds the Global path set from $HOME).
// The read CLIs (fts/jobs/sessions/projects) thus read exactly the DB the agent
// wrote — there is no per-config DB, so they take no --config flag. It ensures
// the data dir exists (0700) so a read CLI invoked before any agent run — when
// EnsureGlobal hasn't created ~/.shell3/data yet — opens cleanly instead of
// failing on a missing parent directory (sqlite creates the file, not the dir).
func canonicalDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	g := paths.NewGlobal(home)
	if err := os.MkdirAll(g.Data, 0o700); err != nil {
		return "", err
	}
	return g.DB, nil
}
