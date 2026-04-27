package edittool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// file is created (errors if it already exists). If newString is empty the
// match is deleted. Line endings are preserved: if the original file is CRLF,
// the replacement is also written CRLF.
//
// workDir resolves a relative filePath.
func EditFile(workDir, filePath, oldString, newString string, replaceAll bool) (Result, error) {
	if filePath == "" {
		return Result{}, errors.New("file_path is required")
	}
	abs := resolvePath(workDir, filePath)

	if oldString == "" {
		if _, err := os.Stat(abs); err == nil {
			return Result{}, fmt.Errorf("file already exists: %s (pass non-empty old_string to edit, or use write_file to overwrite)", abs)
		} else if !os.IsNotExist(err) {
			return Result{}, fmt.Errorf("stat %s: %w", abs, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(abs, []byte(newString), 0o644); err != nil {
			return Result{}, err
		}
		add, del := lineStats("", newString)
		return Result{Path: abs, NewContent: newString, Created: true, Additions: add, Deletions: del}, nil
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, fmt.Errorf("file %s not found", abs)
		}
		return Result{}, err
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("path is a directory, not a file: %s", abs)
	}

	raw, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	original := string(raw)
	mode := info.Mode().Perm()
	ending := detectLineEnding(original)
	normalized := normalizeLineEndings(original)
	old := convertToLineEnding(normalizeLineEndings(oldString), ending)
	new_ := convertToLineEnding(normalizeLineEndings(newString), ending)

	updated, rerr := Replace(original, old, new_, replaceAll)
	if rerr != nil {
		// Fallback: if the file's native ending is CRLF but the model emitted
		// the search/replacement against an LF-normalized version of the
		// content, try matching on the LF-normalized original and re-coerce
		// the output to the source's ending.
		altOld := strings.ReplaceAll(old, "\r\n", "\n")
		altNew := strings.ReplaceAll(new_, "\r\n", "\n")
		if alt, aerr := Replace(normalized, altOld, altNew, replaceAll); aerr == nil {
			updated = convertToLineEnding(alt, ending)
		} else {
			return Result{}, rerr
		}
	}
	if err := os.WriteFile(abs, []byte(updated), mode); err != nil {
		return Result{}, err
	}
	add, del := lineStats(original, updated)
	return Result{Path: abs, OldContent: original, NewContent: updated, Additions: add, Deletions: del}, nil
}

// WriteFile writes content to filePath, creating parent directories. Overwrites.
func WriteFile(workDir, filePath, content string) (Result, error) {
	if filePath == "" {
		return Result{}, errors.New("file_path is required")
	}
	abs := resolvePath(workDir, filePath)
	created := false
	var oldContent string
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return Result{}, fmt.Errorf("path is a directory: %s", abs)
		}
		raw, _ := os.ReadFile(abs)
		oldContent = string(raw)
	} else if os.IsNotExist(err) {
		created = true
	} else {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	add, del := lineStats(oldContent, content)
	return Result{Path: abs, OldContent: oldContent, NewContent: content, Created: created, Additions: add, Deletions: del}, nil
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
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// UnifiedDiff returns a small unified-style preview (max maxLines body lines).
// Not a full unified diff — just a quick visual for tool output. For files
// large enough that DP would blow lcsBudget, returns a stat-only placeholder
// rather than allocating a giant matrix.
func UnifiedDiff(oldContent, newContent string, maxLines int) string {
	a := splitLines(oldContent)
	b := splitLines(newContent)
	m, n := len(a), len(b)
	if m*n > lcsBudget {
		return fmt.Sprintf("  (diff omitted — file too large: %d × %d lines)", m, n)
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
	var ops []string
	i, j := m, n
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			ops = append([]string{"  " + a[i-1]}, ops...)
			i--
			j--
		case dp[i-1][j] >= dp[i][j-1]:
			ops = append([]string{"- " + a[i-1]}, ops...)
			i--
		default:
			ops = append([]string{"+ " + b[j-1]}, ops...)
			j--
		}
	}
	for i > 0 {
		ops = append([]string{"- " + a[i-1]}, ops...)
		i--
	}
	for j > 0 {
		ops = append([]string{"+ " + b[j-1]}, ops...)
		j--
	}
	// Trim to first maxLines that include changes plus 1 line of context each side.
	body := compactDiff(ops, maxLines)
	return strings.Join(body, "\n")
}

func compactDiff(ops []string, maxLines int) []string {
	if maxLines <= 0 || len(ops) <= maxLines {
		return ops
	}
	// Find the first change and emit a window around it.
	first := -1
	for i, op := range ops {
		if strings.HasPrefix(op, "+ ") || strings.HasPrefix(op, "- ") {
			first = i
			break
		}
	}
	if first == -1 {
		return ops[:maxLines]
	}
	start := max(0, first-1)
	end := min(len(ops), start+maxLines)
	out := append([]string{}, ops[start:end]...)
	if end < len(ops) {
		out = append(out, fmt.Sprintf("  … (%d more lines)", len(ops)-end))
	}
	return out
}
