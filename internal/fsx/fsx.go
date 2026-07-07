// Package fsx implements direct-disk text file I/O for the read and
// edit_file tools. It is a leaf package: standard library only, so both
// internal/chat and internal/edittool can import it without a cycle.
package fsx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrIsDir is returned by ReadTextFile when the path is a directory. Callers
// detect it with errors.Is(err, ErrIsDir).
var ErrIsDir = errors.New("is a directory")

// ReadTextFile reads absPath's full contents. Returns os.ErrNotExist if the
// file is missing and ErrIsDir if it is a directory.
func ReadTextFile(_ context.Context, absPath string) (string, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err // includes os.ErrNotExist
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s: %w", absPath, ErrIsDir)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteTextFile creates or overwrites absPath with content, creating parent
// directories as needed. An existing file's permission bits are preserved; a
// new file gets 0644.
func WriteTextFile(_ context.Context, absPath, content string) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(absPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s: %w", absPath, ErrIsDir)
		}
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(absPath, []byte(content), mode)
}
