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
	"strings"
	"text/template"
)

//go:embed all:defaults/base
var baseFS embed.FS

const baseRoot = "defaults/base"

//go:embed all:defaults/telegram
var telegramFS embed.FS

const telegramRoot = "defaults/telegram"

// Values are the user-supplied substitutions for the templated shell3.lua.
type Values struct {
	Name    string // model handle, e.g. "main"
	BaseURL string // OpenAI-compatible endpoint
	EnvKey  string // .env key holding the API key, e.g. "MAIN_API_KEY"
	Model   string // model tag/id
	Proxy   string // optional run_proxy command ("" => commented out)
}

// TelegramValues are the substitutions for the templated telegram shell3.lua.
// It embeds Values (the model block) and adds the telegram host fields.
type TelegramValues struct {
	Values
	ChatID           string // numeric Telegram chat id (goes in the lua)
	WorkDir          string // agent working directory
	DashboardEnabled bool
	DashboardAddr    string // e.g. "127.0.0.1:8765"
	DashboardURL     string // public Mini App URL ("" if none)
}

// RenderBaseConfig writes the base config tree into dir: shell3.lua rendered
// from the embedded template with v, plus the verbatim lib/ modules. When force
// is false, existing files are left untouched (safe to re-run); when true, both
// shell3.lua and the lib/ modules are regenerated, overwriting any local edits.
func RenderBaseConfig(dir string, v Values, force bool) error {
	tmplBytes, err := baseFS.ReadFile(baseRoot + "/shell3.lua.tmpl")
	if err != nil {
		return fmt.Errorf("scaffold: read template: %w", err)
	}
	t, err := template.New("shell3.lua").Funcs(template.FuncMap{"luaesc": luaEscape}).Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("scaffold: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return fmt.Errorf("scaffold: execute template: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "shell3.lua"), buf.Bytes(), 0644, force); err != nil {
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
		return writeFile(filepath.Join(dir, rel), content, 0644, force)
	})
}

// RenderTelegramConfig writes the telegram config tree into dir: shell3.lua
// rendered from the embedded telegram template with v, plus the verbatim lib/
// modules reused from the base scaffold (tools, guards, and the rest). When force
// is false, existing files are left untouched (safe to re-run).
func RenderTelegramConfig(dir string, v TelegramValues, force bool) error {
	tmplBytes, err := telegramFS.ReadFile(telegramRoot + "/shell3.lua.tmpl")
	if err != nil {
		return fmt.Errorf("scaffold: read telegram template: %w", err)
	}
	t, err := template.New("shell3.lua").Funcs(template.FuncMap{"luaesc": luaEscape}).Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("scaffold: parse telegram template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return fmt.Errorf("scaffold: execute telegram template: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "shell3.lua"), buf.Bytes(), 0644, force); err != nil {
		return err
	}
	// Reuse the base lib/ modules (tools, guards, …) verbatim.
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
		return writeFile(filepath.Join(dir, rel), content, 0644, force)
	})
}

// luaEscape escapes a string for safe interpolation inside a double-quoted Lua
// string literal: backslash, double-quote, and line breaks. Onboarding inputs
// (URLs, model tags, proxy commands) can contain these; without escaping a stray
// quote or backslash would produce a config that fails to parse.
func luaEscape(s string) string {
	return luaEscaper.Replace(s)
}

var luaEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// writeFile writes content to path. When force is false it skips an existing
// file (idempotent re-run); when true it overwrites.
func writeFile(path string, content []byte, mode fs.FileMode, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("scaffold: stat %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, mode)
}
