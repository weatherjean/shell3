// Package scaffold renders the split-file base shell3 configuration that
// `shell3 boot` writes for a new install.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed all:defaults/base
var baseFS embed.FS

const baseRoot = "defaults/base"

// Values are the user-supplied substitutions for the templated shell3.lua.
type Values struct {
	Name    string // model handle, e.g. "main"
	BaseURL string // OpenAI-compatible endpoint
	EnvKey  string // .env key holding the API key, e.g. "MAIN_API_KEY"
	Model   string // model tag/id
	Proxy   string // optional run_proxy command ("" => commented out)
}

// RenderBaseConfig writes the base config tree into dir: shell3.lua rendered
// from the embedded template with v, plus the verbatim lib/ modules. Existing
// files are never overwritten (writeIfAbsent), so it is safe to re-run.
func RenderBaseConfig(dir string, v Values) error {
	tmplBytes, err := baseFS.ReadFile(baseRoot + "/shell3.lua.tmpl")
	if err != nil {
		return fmt.Errorf("scaffold: read template: %w", err)
	}
	t, err := template.New("shell3.lua").Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("scaffold: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return fmt.Errorf("scaffold: execute template: %w", err)
	}
	if err := writeIfAbsent(filepath.Join(dir, "shell3.lua"), buf.Bytes(), 0644); err != nil {
		return err
	}

	return fs.WalkDir(baseFS, baseRoot+"/lib", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseRoot, p)
		if err != nil {
			return err
		}
		content, err := baseFS.ReadFile(p)
		if err != nil {
			return err
		}
		return writeIfAbsent(filepath.Join(dir, rel), content, 0644)
	})
}

func writeIfAbsent(path string, content []byte, mode fs.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("scaffold: stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
