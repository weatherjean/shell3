// Package mediadir resolves shell3's durable media directory. It is split
// out from internal/media (rather than living there directly) to break an
// import cycle: internal/media imports internal/shell3 (for the HostTool
// type used by the image_generate tool), and internal/shell3 imports
// internal/agentsetup (to build Parts) — so internal/agentsetup cannot
// import internal/media directly to call SetBaseDir in BuildParts. This
// leaf package has no such dependencies, so both internal/media (which
// re-exports Dir/SetBaseDir for its existing callers) and
// internal/agentsetup can import it directly.
package mediadir

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// baseDir is the config directory whose media/ subdir Dir() serves, set once
// per generation by SetBaseDir (agentsetup.BuildParts). Guarded because
// Reload rebuilds Parts on one goroutine while sessions call Dir() on
// others.
var (
	baseMu  sync.Mutex
	baseDir string
)

// SetBaseDir points Dir() at <configDir>/media. Empty resets to the
// ~/.shell3/media default. Called from agentsetup.BuildParts so a scratch
// --config run stages media beside its own config, not in $HOME.
func SetBaseDir(configDir string) {
	baseMu.Lock()
	baseDir = configDir
	baseMu.Unlock()
}

// Dir returns shell3's durable media directory — where browser uploads,
// generated images, and cached speech are stored, so every media file the
// agent has seen or made keeps a stable path that survives reboots and OS
// temp cleaning (re-readable with read_media, servable at /api/media/,
// findable from history). Default <configDir>/media (which is
// ~/.shell3/media for the default config dir, see SetBaseDir);
// $SHELL3_MEDIA_DIR overrides (tests point it at a TempDir). Created on
// demand.
func Dir() (string, error) {
	dir := os.Getenv("SHELL3_MEDIA_DIR")
	if dir == "" {
		baseMu.Lock()
		if baseDir != "" {
			dir = filepath.Join(baseDir, "media")
		}
		baseMu.Unlock()
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("media: resolving home dir: %w", err)
		}
		dir = filepath.Join(home, ".shell3", "media")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("media: cannot create %s: %w", dir, err)
	}
	return dir, nil
}
