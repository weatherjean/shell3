package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
	"github.com/weatherjean/shell3/internal/scaffold"
)

// EnsureGlobal creates ~/.shell3/ and its subdirectories if missing,
// and writes default global config files (persona, example tool) if absent.
func EnsureGlobal(g paths.Global) error {
	for _, dir := range []string{
		g.Root, g.Skills, g.Tools, g.Hooks, g.Personas, g.Projects,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}
	if err := scaffold.WriteDefaults(g.Personas, g.Tools, g.Skills, g.Hooks); err != nil {
		return fmt.Errorf("bootstrap: write global defaults: %w", err)
	}
	return nil
}

// EnsureProject creates .shell3/ subdirectories and the .ref file for this
// project. Returns the project UUID (creating one on first call).
func EnsureProject(l paths.Local, g paths.Global, cwd string) (string, error) {
	for _, dir := range []string{
		l.Root, l.Skills, l.Tools, l.Hooks, l.Personas,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
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
	if strings.Contains(string(b), ".ref") {
		return nil
	}
	entry := "\n.ref\n"
	if len(b) == 0 {
		entry = ".ref\n"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("bootstrap: open gitignore: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}
