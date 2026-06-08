package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

// EnsureGlobal creates ~/.shell3/ (and its projects/ dir) and the global
// .gitignore if missing. It does NOT write any shell3.lua — config is created
// explicitly via `shell3 boot`.
func EnsureGlobal(g paths.Global) error {
	for _, dir := range []string{g.Root, g.Projects} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}
	if err := ensureGlobalGitignore(g); err != nil {
		return fmt.Errorf("bootstrap: global gitignore: %w", err)
	}
	return nil
}

// EnsureProject creates ./.shell3/ and the .ref file for this project, and the
// project's global state dir. Returns the project UUID (created on first call).
func EnsureProject(l paths.Local, g paths.Global, cwd string) (string, error) {
	if err := os.MkdirAll(l.Root, 0755); err != nil {
		return "", fmt.Errorf("bootstrap: mkdir %s: %w", l.Root, err)
	}

	if err := ensureGitignore(l); err != nil {
		return "", err
	}

	id, err := ref.Init(l, g, cwd)
	if err != nil {
		return "", fmt.Errorf("bootstrap: ref init: %w", err)
	}
	p := paths.NewProject(g, id)
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return "", fmt.Errorf("bootstrap: mkdir project dir: %w", err)
	}
	return id, nil
}

func ensureGitignore(l paths.Local) error {
	path := filepath.Join(l.Root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: read gitignore: %w", err)
	}

	// Each of these must appear as its own whole line; substring matches
	// (e.g. "*.reference" or a "# don't commit bg.json" comment) do not count.
	missing := missingLines(string(b), ".ref", "bg.json", "proxy-*.log")
	if len(missing) == 0 {
		return nil
	}

	return appendGitignore("", path, string(b), strings.Join(missing, "\n")+"\n")
}

// appendGitignore appends addition to the .gitignore at path, given the file's
// current content. It guards the leading newline so the addition always starts
// on its own line, and creates the file with mode 0644 when absent. label (e.g.
// "global ") is interpolated into error messages to distinguish the two
// gitignore writers; pass "" for the project gitignore.
func appendGitignore(label, path, content, addition string) error {
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		addition = "\n" + addition
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("bootstrap: open %sgitignore: %w", label, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("bootstrap: write %sgitignore: %w", label, err)
	}
	return nil
}

// missingLines returns the subset of want that does not already appear as a
// whole trimmed line in content, preserving the order given in want.
func missingLines(content string, want ...string) []string {
	have := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// ensureGlobalGitignore creates or updates ~/.shell3/.gitignore to ignore
// files that should never be committed even when ~/.shell3/ is tracked in a
// dotfiles repo: credentials, secrets, logs (including rotated archives).
func ensureGlobalGitignore(g paths.Global) error {
	path := filepath.Join(g.Root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: read global gitignore: %w", err)
	}
	content := string(b)

	// Sentinel: if the log pattern is already there as its own line, nothing
	// to do. A whole-line match avoids false positives from substrings such
	// as a "shell3.log.*" archive pattern or a comment mentioning the file.
	if len(missingLines(content, "shell3.log")) == 0 {
		return nil
	}

	return appendGitignore("global ", path, content, globalGitignoreAddition)
}

// globalGitignoreAddition is appended to ~/.shell3/.gitignore when the log
// sentinel is absent. Covers credentials, secrets, and all log files
// (current + rotated archives shell3.log.1 through shell3.log.N).
const globalGitignoreAddition = `# shell3 — never commit these even in a dotfiles repo
ai-do-not-read.*
.env
shell3.log
shell3.log.*
projects/
`
