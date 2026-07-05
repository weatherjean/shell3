package chat

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// defaultListDepth keeps the first listing shallow — the model widens it with
	// an explicit depth or narrows with a subdirectory path / ignore globs.
	defaultListDepth = 2
	// maxListFiles caps how many entries one call returns, so listing a huge tree
	// can't flood the context; the output ends with a truncation notice.
	maxListFiles = 1000
)

type listFilesArgs struct {
	Path   string   `json:"path"`
	Depth  int      `json:"depth"`
	Ignore []string `json:"ignore"`
}

// handleListFilesTool lists a directory as an indented tree (directories first,
// suffixed "/"). It does NO automatic filtering — hidden and vendored files are
// shown unless excluded via ignore globs — so a no-bash read-only agent can map
// the filesystem and then read specific files.
func handleListFilesTool(argsJSON, workDir string) string {
	var a listFilesArgs
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "error: invalid list_files arguments: " + err.Error()
	}
	depth := a.Depth
	if depth <= 0 {
		depth = defaultListDepth
	}
	root := resolveReadPath(strings.TrimSpace(a.Path), workDir) // reuse read's ~ + relative resolution

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "error: directory not found: " + cmp.Or(a.Path, ".")
		}
		return "error: " + err.Error()
	}
	if !info.IsDir() {
		return "error: " + cmp.Or(a.Path, ".") + " is a file, not a directory; use the read tool"
	}
	// Surface a root-level read error (e.g. a directory with execute- but not
	// read-permission) explicitly. walkTree intentionally swallows per-subdir
	// read errors mid-walk, but the directory the user asked for shouldn't be
	// silently reported as empty.
	if _, err := os.ReadDir(root); err != nil {
		return "error: " + err.Error()
	}

	out, truncated := listTree(root, depth, a.Ignore, maxListFiles)
	if strings.TrimSpace(out) == "" {
		return "(empty directory)"
	}
	if truncated {
		out += fmt.Sprintf("\n[Truncated at %d entries. Narrow with a subdirectory `path`, an `ignore` glob, or a lower `depth`.]", maxListFiles)
	}
	return out
}

// listTree renders absRoot's contents as an indented tree up to maxDepth levels,
// excluding entries matching any ignore glob, capped at limit entries. The bool
// reports whether the cap was hit.
func listTree(absRoot string, maxDepth int, ignore []string, limit int) (string, bool) {
	var b strings.Builder
	count := 0
	truncated := walkTree(&b, absRoot, "", 1, maxDepth, ignore, &count, limit)
	return b.String(), truncated
}

// walkTree appends one indented line per entry in dirAbs (its path relative to
// the root is relPrefix), recursing into subdirectories until curDepth reaches
// maxDepth. Symlinks are listed but never traversed (DirEntry.IsDir is false for
// them), so symlink loops can't hang the walk. Returns true if the entry cap hit.
func walkTree(b *strings.Builder, dirAbs, relPrefix string, curDepth, maxDepth int, ignore []string, count *int, limit int) bool {
	entries, err := os.ReadDir(dirAbs)
	if err != nil {
		return false // unreadable dir (permissions): skip silently, like a tree walk
	}
	// Directories first, then files; each group alphabetical.
	slices.SortStableFunc(entries, func(a, b os.DirEntry) int {
		if da, db := a.IsDir(), b.IsDir(); da != db {
			if da {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})

	indent := strings.Repeat("  ", curDepth-1)
	for _, e := range entries {
		name := e.Name()
		rel := name
		if relPrefix != "" {
			rel = relPrefix + "/" + name
		}
		if ignored(ignore, name, rel) {
			continue
		}
		if *count >= limit {
			return true
		}
		if e.IsDir() {
			fmt.Fprintf(b, "%s%s/\n", indent, name)
		} else {
			fmt.Fprintf(b, "%s%s\n", indent, name)
		}
		*count++
		if e.IsDir() && curDepth < maxDepth {
			if walkTree(b, filepath.Join(dirAbs, name), rel, curDepth+1, maxDepth, ignore, count, limit) {
				return true
			}
		}
	}
	return false
}

// ignored reports whether name or its root-relative path matches any glob. A
// pattern with no "/" is matched against the base name; otherwise against the
// relative path. Invalid patterns never match (rather than erroring the call).
func ignored(patterns []string, name, rel string) bool {
	for _, p := range patterns {
		target := name
		if strings.Contains(p, "/") {
			target = rel
		}
		if ok, err := path.Match(p, target); err == nil && ok {
			return true
		}
	}
	return false
}
