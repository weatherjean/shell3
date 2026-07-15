package edittool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// Result reports what an edit/write produced so callers can render stats.
type Result struct {
	Path       string
	OldContent string
	NewContent string
	Created    bool
	Additions  int
	Deletions  int
}

// EditFile applies a str-replace edit to filePath. If oldString is empty the
// file is created or overwritten with newString. If newString is empty the
// match is deleted. Line endings are preserved: if the original file is CRLF,
// the replacement is also written CRLF.
//
// workDir resolves a relative filePath. I/O always hits the real disk.
func EditFile(ctx context.Context, workDir, filePath, oldString, newString string, replaceAll bool) (Result, error) {
	if filePath == "" {
		return Result{}, errors.New("file_path is required")
	}
	abs := resolvePath(workDir, filePath)

	if oldString == "" {
		oldContent, rerr := readTextFile(abs)
		created := false
		switch {
		case rerr == nil:
			created = false
		case errors.Is(rerr, os.ErrNotExist):
			oldContent, created = "", true
		case errors.Is(rerr, errIsDir):
			return Result{}, fmt.Errorf("path is a directory, not a file: %s", abs)
		default:
			return Result{}, rerr
		}
		if err := writeTextFile(abs, newString); err != nil {
			return Result{}, err
		}
		add, del := lineStats(oldContent, newString)
		return Result{Path: abs, OldContent: oldContent, NewContent: newString, Created: created, Additions: add, Deletions: del}, nil
	}

	original, err := readTextFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("file %s not found", abs)
		}
		if errors.Is(err, errIsDir) {
			return Result{}, fmt.Errorf("path is a directory, not a file: %s", abs)
		}
		return Result{}, err
	}
	ending := detectLineEnding(original)
	old := convertToLineEnding(normalizeLineEndings(oldString), ending)
	newStr := convertToLineEnding(normalizeLineEndings(newString), ending)

	updated, rerr := replace(original, old, newStr, replaceAll)
	if errors.Is(rerr, errNotFound) {
		// Fallback ONLY when the search text wasn't found: if the file's native
		// ending is CRLF but the model emitted the search/replacement against an
		// LF-normalized version of the content, try matching on the LF-normalized
		// original and re-coerce the output to the source's ending. We do not run
		// this on errMultipleMatch (it could collapse an ambiguous match into a
		// false unique one) or errNoChange.
		altOld := strings.ReplaceAll(old, "\r\n", "\n")
		altNew := strings.ReplaceAll(newStr, "\r\n", "\n")
		if alt, aerr := replace(normalizeLineEndings(original), altOld, altNew, replaceAll); aerr == nil {
			updated, rerr = convertToLineEnding(alt, ending), nil
		}
	}
	if rerr != nil {
		return Result{}, rerr
	}
	if err := writeTextFile(abs, updated); err != nil {
		return Result{}, err
	}
	add, del := lineStats(original, updated)
	return Result{Path: abs, OldContent: original, NewContent: updated, Additions: add, Deletions: del}, nil
}

func resolvePath(workDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if workDir == "" {
		return p
	}
	return filepath.Join(workDir, p)
}

func normalizeLineEndings(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func detectLineEnding(s string) string {
	if strings.Contains(s, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func convertToLineEnding(s, ending string) string {
	if ending == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// lcsBudget caps O(n*m) DP allocation. Above this product we fall back to a
// multiset-difference approximation that is monotonic and order-insensitive
// but cheap. Two million ints ≈ 16 MiB at 8 bytes — survives big edits without
// surprising the user with multi-second pauses or memory spikes.
const lcsBudget = 2_000_000

// lineStats returns (additions, deletions) computed against a line-level LCS
// when the file is small enough; otherwise a multiset-difference fallback.
// Empty input on either side counts every line on the other side.
func lineStats(oldContent, newContent string) (int, int) {
	a := splitLines(oldContent)
	b := splitLines(newContent)
	if len(a) == 0 {
		return len(b), 0
	}
	if len(b) == 0 {
		return 0, len(a)
	}
	m, n := len(a), len(b)
	if m*n > lcsBudget {
		return approxLineStats(a, b)
	}
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	lcs := dp[m][n]
	return n - lcs, m - lcs
}

// approxLineStats counts adds/deletes by treating each side as a multiset of
// lines. Misses reorders (a moved line shows as +1 -1 instead of 0 0) but is
// O(m+n) and never under-counts; good enough for a stat header on huge files.
func approxLineStats(a, b []string) (int, int) {
	count := make(map[string]int, len(a))
	for _, l := range a {
		count[l]++
	}
	additions := 0
	for _, l := range b {
		if count[l] > 0 {
			count[l]--
		} else {
			additions++
		}
	}
	deletions := 0
	for _, c := range count {
		deletions += c
	}
	return additions, deletions
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(normalizeLineEndings(s), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// UnifiedDiff returns a unified diff preview with the requested number of
// unchanged context lines around every changed hunk. Unlike the chat-level
// generic tool-output truncation, this intentionally includes every hunk so
// distant edits do not disappear from the user's view.
func UnifiedDiff(oldContent, newContent string, contextLines int) string {
	oldNormalized := normalizeLineEndings(oldContent)
	newNormalized := normalizeLineEndings(newContent)
	diff, err := udiff.ToUnified("old", "new", oldNormalized, udiff.Lines(oldNormalized, newNormalized), contextLines)
	if err != nil {
		return ""
	}
	return stripDiffFileHeaders(strings.TrimRight(diff, "\n"))
}

func stripDiffFileHeaders(diff string) string {
	lines := strings.Split(diff, "\n")
	if len(lines) >= 2 && strings.HasPrefix(lines[0], "--- ") && strings.HasPrefix(lines[1], "+++ ") {
		lines = lines[2:]
	}
	return strings.Join(lines, "\n")
}
