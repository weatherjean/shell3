package usertools

import (
	"sort"
	"strings"
)

// minSecretLen is the shortest value we'll attempt to redact. Below this,
// values are too likely to occur incidentally in normal output and would
// cause more harm (corrupted output) than good.
const minSecretLen = 4

// Redact replaces each occurrence of any secretValue in s with
// "***REDACTED***". Empty or very short values are skipped. Secrets are
// processed longest-first so a long secret containing a shorter secret as
// a substring is redacted before the shorter one would mangle it.
func Redact(s string, secretValues []string) string {
	if len(secretValues) == 0 {
		return s
	}
	sorted := make([]string, 0, len(secretValues))
	for _, v := range secretValues {
		if len(v) >= minSecretLen {
			sorted = append(sorted, v)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		s = strings.ReplaceAll(s, v, "***REDACTED***")
	}
	return s
}
