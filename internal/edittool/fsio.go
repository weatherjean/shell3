package edittool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// errIsDir is returned by readTextFile when the path is a directory. Callers
// detect it with errors.Is(err, errIsDir).
var errIsDir = errors.New("is a directory")

// readTextFile reads absPath's full contents. Returns os.ErrNotExist if the
// file is missing and errIsDir if it is a directory.
func readTextFile(absPath string) (string, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err // includes os.ErrNotExist
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s: %w", absPath, errIsDir)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeTextFile creates or overwrites absPath with content, creating parent
// directories as needed. An existing file's permission bits are preserved; a
// new file gets 0644.
func writeTextFile(absPath, content string) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(absPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s: %w", absPath, errIsDir)
		}
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(absPath, []byte(content), mode)
}
