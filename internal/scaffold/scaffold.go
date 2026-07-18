// Package scaffold renders the base shell3 config directory that
// `shell3 boot` writes for a new install: shell3.yaml + agent.md rendered
// from embedded templates, plus the verbatim agents/, skills/, hooks/, and
// lib/ files.
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

// Values are the user-supplied substitutions for the templated files.
type Values struct {
	Name    string // model handle, e.g. "main"
	BaseURL string // OpenAI-compatible endpoint
	EnvKey  string // .env key holding the API key, e.g. "MAIN_API_KEY"
	Model   string // model tag/id
	Proxy   string // optional run_proxy command ("" => commented out)
	ChatID  string // Telegram chat id the bot answers ("" renders chat_id: "")

	// Vision reports whether the model can see images. True wires
	// media.describe to the main model (inbound Telegram images get captioned
	// out of the box) and enables the agent's media tool; false leaves both as
	// commented hints.
	Vision bool

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

// RenderBaseConfig writes the base config tree into dir: every *.tmpl in the
// embedded tree rendered with v (dropping the extension), every other file
// verbatim. When force is false, existing files are left untouched (safe to
// re-run); when true, everything is regenerated, overwriting local edits.
func RenderBaseConfig(dir string, v Values, force bool) error {
	v = v.withDefaults()
	return fs.WalkDir(baseFS, baseRoot, func(p string, d fs.DirEntry, err error) error {
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
		if strings.HasSuffix(rel, ".tmpl") {
			rendered, err := renderTemplate(rel, content, v)
			if err != nil {
				return err
			}
			content, rel = rendered, strings.TrimSuffix(rel, ".tmpl")
		}
		return writeFile(filepath.Join(dir, rel), content, force)
	})
}

// renderTemplate executes one embedded template with v.
func renderTemplate(name string, tmpl []byte, v Values) ([]byte, error) {
	t, err := template.New(name).Funcs(template.FuncMap{
		"yamlstr": yamlString,
		"yamlkey": yamlKey,
	}).Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("scaffold: parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return nil, fmt.Errorf("scaffold: execute %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// yamlString renders s as a double-quoted YAML scalar, escaping backslash,
// double-quote, and line breaks. Onboarding inputs (URLs, model tags, proxy
// commands) can contain these; without escaping a stray quote would produce a
// config that fails to parse.
func yamlString(s string) string {
	return `"` + yamlEscaper.Replace(s) + `"`
}

var yamlEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// yamlKey renders a model handle used as a YAML mapping key or bare reference.
// Boot validates handles to a safe charset; this quotes anything that slipped
// through with characters YAML would misread bare.
func yamlKey(s string) string {
	if strings.ContainsAny(s, ":{}[],&*#?|-<>=!%@\"' \t") || s == "" {
		return yamlString(s)
	}
	return s
}

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
	// Directories are 0700: everything scaffold writes lives under ~/.shell3,
	// which also holds the .env secrets file — the user-private parent gates
	// access even though the files themselves are 0644, matching
	// bootstrap.EnsureGlobal (which creates ~/.shell3 at 0700).
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("scaffold: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("scaffold: write %s: %w", path, err)
	}
	return nil
}
