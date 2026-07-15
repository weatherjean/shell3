package chat

import (
	"os"
	"path/filepath"
	"strings"
)

// resolvePath expands ~ and resolves a relative path against workDir. Used by
// the media loaders (read_media) to resolve model-supplied paths.
func resolvePath(p, workDir string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}
