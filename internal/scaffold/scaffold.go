// Package scaffold writes default configuration files for new shell3 projects.
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPersonaName is the persona loaded when no --persona flag is given.
const DefaultPersonaName = "base"

//go:embed defaults/**
var defaultFiles embed.FS

type defaultDir struct {
	src string
	dst string
}

// WriteDefaults writes the embedded default bootstrap configuration if files do
// not already exist. Safe to call on every run — existing files are preserved.
func WriteDefaults(personasDir, toolsDir, skillsDir, hooksDir string) error {
	for _, dir := range []defaultDir{
		{src: "defaults/personas", dst: personasDir},
		{src: "defaults/tools", dst: toolsDir},
		{src: "defaults/skills", dst: skillsDir},
		{src: "defaults/hooks", dst: hooksDir},
	} {
		if err := writeDefaultDir(dir.src, dir.dst); err != nil {
			return err
		}
	}
	return nil
}

func writeDefaultDir(srcDir, dstDir string) error {
	return fs.WalkDir(defaultFiles, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("scaffold: rel %s: %w", path, err)
		}
		data, err := defaultFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("scaffold: read default %s: %w", path, err)
		}
		mode := fileModeForDefault(path)
		if err := writeIfAbsent(filepath.Join(dstDir, rel), data, mode); err != nil {
			return fmt.Errorf("scaffold: write %s: %w", rel, err)
		}
		return nil
	})
}

func fileModeForDefault(path string) fs.FileMode {
	if strings.HasPrefix(path, "defaults/hooks/") {
		return 0755
	}
	return 0644
}

func writeIfAbsent(path string, content []byte, mode fs.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
