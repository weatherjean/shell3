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

	// ContextWindow is the model's token budget; CompactAt is the prompt-token
	// threshold for host-enforced auto-compaction. Both are model-specific —
	// boot prompts for them. Zero values are filled by withDefaults at render.
	ContextWindow int
	CompactAt     int
}

// DefaultContextWindow is the fallback model context window (tokens) when boot
// is non-interactive and no --context-window flag is given.
const DefaultContextWindow = 128000

// withDefaults fills zero ContextWindow/CompactAt with sane values so callers
// (and tests) that omit them still render a valid config. CompactAt defaults to
// 80% of the context window, leaving headroom for the post-compaction turn.
func (v Values) withDefaults() Values {
	if v.ContextWindow <= 0 {
		v.ContextWindow = DefaultContextWindow
	}
	if v.CompactAt <= 0 {
		v.CompactAt = v.ContextWindow * 80 / 100
	}
	return v
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
	v = v.withDefaults()
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
	if err := writeFile(filepath.Join(dir, "shell3.lua"), buf.Bytes(), force); err != nil {
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
		return writeFile(filepath.Join(dir, rel), content, force)
	})
}

// RenderTelegramConfig writes the telegram config tree into dir: shell3.lua
// rendered from the embedded telegram template with v, plus the verbatim lib/
// modules reused from the base scaffold (tools and the rest). When force
// is false, existing files are left untouched (safe to re-run).
func RenderTelegramConfig(dir string, v TelegramValues, force bool) error {
	v.Values = v.withDefaults()
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
	if err := writeFile(filepath.Join(dir, "shell3.lua"), buf.Bytes(), force); err != nil {
		return err
	}
	// Reuse the base lib/ modules (tools, …) verbatim.
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
		return writeFile(filepath.Join(dir, rel), content, force)
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
func writeFile(path string, content []byte, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("scaffold: stat %s: %w", path, err)
		}
	}
	// 0700: everything scaffold writes lives under ~/.shell3, which also holds
	// the .env secrets file — keep the whole tree user-private and consistent
	// with bootstrap.EnsureGlobal (which creates ~/.shell3 at 0700).
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}
