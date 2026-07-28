package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFile is one resolved `context:` entry: Path is the config-dir-relative
// path used for its `### <path>` prompt heading (so the agent knows where to
// edit_file its own brain), Body is the file's contents (or a one-line stub
// when a literal file has disappeared since load).
type ContextFile struct {
	Path string
	Body string
}

// isContextGlob reports whether entry is a glob pattern (vs a literal path).
// Mirrors the metacharacters filepath.Match understands.
func isContextGlob(entry string) bool {
	return strings.ContainsAny(entry, "*?[")
}

// ResolveContextFiles reads the main agent's `context:` entries against
// configDir, in list order (a glob's matches sorted lexically within its
// entry). A literal entry that has vanished since config load yields a stub
// body rather than an error — a missing brain file must never fail a turn.
// The only error path is a malformed glob pattern (already rejected at load).
func ResolveContextFiles(configDir string, entries []string) ([]ContextFile, error) {
	var out []ContextFile
	for _, e := range entries {
		if isContextGlob(e) {
			matches, err := filepath.Glob(filepath.Join(configDir, e))
			if err != nil {
				return nil, fmt.Errorf("context glob %q: %w", e, err)
			}
			sort.Strings(matches)
			for _, m := range matches {
				rel := relForPrompt(configDir, m)
				out = append(out, readContextFile(m, rel))
			}
			continue
		}
		out = append(out, readContextFile(filepath.Join(configDir, e), e))
	}
	return out, nil
}

// readContextFile reads abs, tagging the result with the config-dir-relative
// rel; an unreadable file (e.g. deleted between load and session build) yields
// the missing-file stub instead of an error.
func readContextFile(abs, rel string) ContextFile {
	body, err := os.ReadFile(abs)
	if err != nil {
		return ContextFile{Path: rel, Body: "(context file missing: " + rel + ")"}
	}
	return ContextFile{Path: rel, Body: string(body)}
}

// relForPrompt renders m relative to configDir with forward slashes, falling
// back to m if it somehow escapes configDir.
func relForPrompt(configDir, m string) string {
	rel, err := filepath.Rel(configDir, m)
	if err != nil {
		return m
	}
	return filepath.ToSlash(rel)
}

// validateContextEntries enforces the load-time rules for the main agent's
// `context:` list: a literal (non-glob) entry must exist (missing = load
// error, strict tradition); a glob that matches nothing is legal but records a
// warning (shell3 health hardens it into a failure). A malformed glob pattern
// is a load error.
func validateContextEntries(dir string, entries []string, warn func(string)) error {
	for _, e := range entries {
		if isContextGlob(e) {
			matches, err := filepath.Glob(filepath.Join(dir, e))
			if err != nil {
				return fmt.Errorf("agent.md: context glob %q: %w", e, err)
			}
			if len(matches) == 0 {
				warn(fmt.Sprintf("agent.md: context glob %q matched no files", e))
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e)); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("agent.md: context file %q does not exist", e)
			}
			return fmt.Errorf("agent.md: context file %q: %w", e, err)
		}
	}
	return nil
}
