// Package mediadir resolves the durable directory for Telegram attachments.
package mediadir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns $SHELL3_MEDIA_DIR or ~/.shell3/media and creates it on demand.
func Dir() (string, error) {
	dir := os.Getenv("SHELL3_MEDIA_DIR")
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
