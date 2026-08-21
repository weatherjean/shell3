package config

import (
	"os"
	"path/filepath"
	"strings"
)

// mdFile is one *.md read out of a config subdirectory: its base name without
// the extension, and its contents.
type mdFile struct {
	Name string
	Data []byte
}

// readMDFiles reads every flat *.md in dir, in filename order. Subdirectories
// and non-.md files are skipped; a missing dir yields no files and no error
// (an absent optional directory disables its feature).
func readMDFiles(dir string) ([]mdFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []mdFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, mdFile{Name: strings.TrimSuffix(e.Name(), ".md"), Data: data})
	}
	return out, nil
}
