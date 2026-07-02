package fsx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// OS is the default FileSystem backend: direct disk I/O. It owns the
// directory-detection, parent-mkdir, and mode-preservation logic that the read
// and edit_file tools used to do inline.
type OS struct{}

// ReadTextFile reads absPath's full contents. Returns os.ErrNotExist if the
// file is missing and ErrIsDir if it is a directory.
func (OS) ReadTextFile(_ context.Context, absPath string) (string, error) {
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
func (OS) WriteTextFile(_ context.Context, absPath, content string) error {
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
