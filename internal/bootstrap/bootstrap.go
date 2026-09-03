package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/paths"
)

// EnsureProject creates .shell3_project/runs/ under cwd and writes a
// self-ignoring .gitignore ("*") at the root of .shell3_project/ so the whole
// runtime directory is ignored by any enclosing git repo WITHOUT touching that
// repo's own .gitignore. Idempotent.
func EnsureProject(l paths.Local) error {
	if err := os.MkdirAll(l.Runs, 0755); err != nil {
		return fmt.Errorf("bootstrap: mkdir %s: %w", l.Runs, err)
	}
	if err := ensureSelfGitignore(l.Root); err != nil {
		return fmt.Errorf("bootstrap: project gitignore: %w", err)
	}
	return nil
}

// ensureSelfGitignore writes a .gitignore containing "*" at root so the entire
// .shell3_project/ folder is ignored from within. Self-contained: an enclosing
// repo needs no entry of its own. Idempotent — skips the write when "*" is
// already present.
func ensureSelfGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err == nil {
		if hasLine(string(b), "*") {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: read project gitignore: %w", err)
	}
	if err := os.WriteFile(path, []byte("*\n"), 0644); err != nil {
		return fmt.Errorf("bootstrap: write project gitignore: %w", err)
	}
	return nil
}

// hasLine reports whether want appears as a whole trimmed line in content — a
// whole-line match avoids false positives from substrings such as an archive
// pattern or a comment mentioning the file.
func hasLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
