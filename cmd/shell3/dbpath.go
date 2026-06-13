//go:build unix

package main

import (
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/paths"
)

// canonicalDBPath resolves the single canonical DB. With --config it anchors to
// the nearest ".shell3" ancestor of the config (so ~/.shell3/shell3.lua and
// ~/.shell3/telegram/shell3.lua both map to ~/.shell3/data); otherwise it uses
// $HOME, matching the runtime.
func canonicalDBPath(configFlag string) (string, error) {
	if configFlag != "" {
		if root := nearestShell3Dir(configFlag); root != "" {
			return filepath.Join(root, "data", "shell3.db"), nil
		}
		return filepath.Join(filepath.Dir(configFlag), "data", "shell3.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
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
