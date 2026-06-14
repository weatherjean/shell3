//go:build unix

package main

import (
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/paths"
)

// canonicalDBPath resolves the single canonical DB. The --config flag is first
// run through agentsetup.ExpandConfigName (a bare name like "code" becomes
// ~/.shell3/code.lua; a literal *.lua path is left as-is), then anchored to the
// nearest ".shell3" ancestor of that path (so ~/.shell3/shell3.lua and
// ~/.shell3/telegram/shell3.lua both map to ~/.shell3/data); with no flag it
// uses $HOME, matching the runtime.
func canonicalDBPath(configFlag string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configFlag = agentsetup.ExpandConfigName(configFlag, home)
	if configFlag != "" {
		if root := nearestShell3Dir(configFlag); root != "" {
			return filepath.Join(root, "data", "shell3.db"), nil
		}
		return filepath.Join(filepath.Dir(configFlag), "data", "shell3.db"), nil
	}
	return paths.NewGlobal(home).DB, nil
}

func nearestShell3Dir(p string) string {
	for d := filepath.Dir(p); ; {
		if filepath.Base(d) == ".shell3" {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
