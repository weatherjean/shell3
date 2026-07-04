// Package edittool ports the opencode str-replace edit algorithm to Go:
// 9 replacer strategies tried in order, each yielding candidate substrings of
// the file content that match the requested old_string. The first replacer
// that yields a uniquely-locatable match wins.
//
// Credit: this is a direct Go port of opencode's edit tool, licensed under
// opencode's terms. See:
//
//	https://github.com/sst/opencode/blob/main/packages/opencode/src/tool/edit.ts
//
// opencode in turn cites:
//   - https://github.com/cline/cline (diff-apply)
//   - https://github.com/google-gemini/gemini-cli (editCorrector)
//
// Error strings here are model-facing tool output, deliberately unprefixed
// (no "edittool:" wrapping) — they are shown to the LLM verbatim as guidance.
package edittool

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

var (
	errNotFound      = errors.New("could not find old_string in file. It must match exactly, including whitespace, indentation, and line endings")
	errMultipleMatch = errors.New("found multiple matches for old_string. Provide more surrounding context to make the match unique")
	errNoChange      = errors.New("no changes to apply: old_string and new_string are identical")
)

// replacer yields candidate matches inside content for the given find string.
// Candidates may differ from find (e.g. with original whitespace restored).
type replacer func(content, find string) []string

// replace applies the replacer cascade and returns the modified content.
// Tries each replacer in order; the first candidate that resolves to exactly
// one occurrence in the source (or any occurrence when replaceAll is true)
// wins. If no replacer yields a candidate, returns errNotFound. If candidates
// were found but every one was ambiguous, returns errMultipleMatch.
//
// When replaceAll is true the first replacer with any occurring candidate wins
// and ALL of its distinct occurring candidates are replaced; ambiguity is
// expected, so this path returns only success or errNotFound (never
// errMultipleMatch).
func replace(content, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == newString {
		return "", errNoChange
	}
	notFound := true
	for _, r := range replacers {
		candidates := r(content, oldString)
		if replaceAll {
			// Replace-all must apply EVERY distinct candidate this replacer found
			// that occurs in content — not just the first. Fuzzy replacers return
			// several distinct substrings (e.g. blocks at different indentation);
			// using only the first silently leaves the others unreplaced.
			var occurring []string
			for _, search := range candidates {
				if strings.Contains(content, search) {
					occurring = append(occurring, search)
				}
			}
			if len(occurring) == 0 {
				continue
			}
			return replaceAllOccurrences(content, occurring, newString), nil
		}
		for _, search := range candidates {
			idx := strings.Index(content, search)
			if idx == -1 {
				continue
			}
			notFound = false
			last := strings.LastIndex(content, search)
			if idx != last {
				continue
			}
			return content[:idx] + newString + content[idx+len(search):], nil
		}
	}
	if notFound {
		return "", errNotFound
	}
	return "", errMultipleMatch
}

// replaceAllOccurrences replaces every occurrence of every distinct candidate
// substring with newString in one left-to-right pass over content. Overlapping
// matches are resolved earliest-first (a later span that starts inside an
// already-replaced span is skipped), and the replacement text is never itself
// rescanned, so no double-replacement can occur.
func replaceAllOccurrences(content string, candidates []string, newString string) string {
	type span struct{ start, end int }
	var spans []span
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		for start := 0; ; {
			idx := strings.Index(content[start:], c)
			if idx == -1 {
				break
			}
			at := start + idx
			spans = append(spans, span{at, at + len(c)})
			start = at + len(c)
		}
	}
	if len(spans) == 0 {
		return content
	}
	// Total order: earliest start first, and on a tie the longest span wins.
	// A total order (not just start) keeps the result deterministic when two
	// distinct candidates begin at the same offset.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var b strings.Builder
	last := 0
	for _, s := range spans {
		if s.start < last {
			continue // overlaps an already-applied span
		}
		b.WriteString(content[last:s.start])
		b.WriteString(newString)
		last = s.end
	}
	b.WriteString(content[last:])
	return b.String()
}

var replacers = []replacer{
	simpleReplacer,
	lineTrimmedReplacer,
	blockAnchorReplacer,
	whitespaceNormalizedReplacer,
	indentationFlexibleReplacer,
	escapeNormalizedReplacer,
	trimmedBoundaryReplacer,
	contextAwareReplacer,
	multiOccurrenceReplacer,
}

// simpleReplacer yields find unchanged.
func simpleReplacer(_ string, find string) []string {
	return []string{find}
}

// lineTrimmedReplacer matches lines after .trim() — handles trailing/leading
// whitespace differences per line. Returns the original (un-trimmed) substring.
func lineTrimmedReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if n := len(searchLines); n > 0 && searchLines[n-1] == "" {
		searchLines = searchLines[:n-1]
	}
	if len(searchLines) == 0 {
		return nil
	}
	var out []string
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true
		for j := 0; j < len(searchLines); j++ {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		start := 0
		for k := 0; k < i; k++ {
			start += len(originalLines[k]) + 1
		}
		end := start
		for k := 0; k < len(searchLines); k++ {
			end += len(originalLines[i+k])
			if k < len(searchLines)-1 {
				end++
			}
		}
		out = append(out, content[start:end])
	}
	return out
}

// With multiple candidates, require ≥30% average middle-line similarity to
// disambiguate; otherwise the match is too speculative. A single candidate
// (one place in the file where both first and last anchor lines match) is
// always accepted: the anchor pair is already a strong signal, and the
// common case is the model getting the framing lines right while rewriting
// everything in between.
const multipleCandidatesThreshold = 0.3

// blockAnchorReplacer matches multi-line blocks where first and last lines
// match (after trim), middle lines are scored by levenshtein similarity.
func blockAnchorReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if len(searchLines) < 3 {
		return nil
	}
	if n := len(searchLines); searchLines[n-1] == "" {
		searchLines = searchLines[:n-1]
	}
	if len(searchLines) < 3 {
		return nil
	}
	first := strings.TrimSpace(searchLines[0])
	last := strings.TrimSpace(searchLines[len(searchLines)-1])
	searchBlockSize := len(searchLines)

	type cand struct{ start, end int }
	var candidates []cand
	for i := 0; i < len(originalLines); i++ {
		if strings.TrimSpace(originalLines[i]) != first {
			continue
		}
		for j := i + 2; j < len(originalLines); j++ {
			if strings.TrimSpace(originalLines[j]) == last {
				candidates = append(candidates, cand{i, j})
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	emit := func(c cand) string {
		start := 0
		for k := 0; k < c.start; k++ {
			start += len(originalLines[k]) + 1
		}
		end := start
		for k := c.start; k <= c.end; k++ {
			end += len(originalLines[k])
			if k < c.end {
				end++
			}
		}
		return content[start:end]
	}

	if len(candidates) == 1 {
		return []string{emit(candidates[0])}
	}

	bestIdx := -1
	maxSim := -1.0
	for i, c := range candidates {
		actualBlockSize := c.end - c.start + 1
		similarity := 0.0
		linesToCheck := min(searchBlockSize-2, actualBlockSize-2)
		if linesToCheck > 0 {
			for j := 1; j < searchBlockSize-1 && j < actualBlockSize-1; j++ {
				ol := strings.TrimSpace(originalLines[c.start+j])
				sl := strings.TrimSpace(searchLines[j])
				maxLen := max(len(ol), len(sl))
				if maxLen == 0 {
					continue
				}
				dist := levenshtein(ol, sl)
				similarity += 1 - float64(dist)/float64(maxLen)
			}
			similarity /= float64(linesToCheck)
		} else {
			similarity = 1.0
		}
		if similarity > maxSim {
			maxSim = similarity
			bestIdx = i
		}
	}
	if maxSim >= multipleCandidatesThreshold && bestIdx >= 0 {
		return []string{emit(candidates[bestIdx])}
	}
	return nil
}

var wsRun = regexp.MustCompile(`\s+`)

// whitespaceNormalizedReplacer collapses whitespace runs to single space.
func whitespaceNormalizedReplacer(content, find string) []string {
	norm := func(s string) string { return strings.TrimSpace(wsRun.ReplaceAllString(s, " ")) }
	normalizedFind := norm(find)
	var out []string

	// The fuzzy-match regexp depends only on find, not on any line, so compile
	// it once up front. re stays nil when find has no words, which disables the
	// per-line Contains branch below, exactly as the per-line guards did.
	// QuoteMeta escapes regex metacharacters, but it cannot rescue invalid
	// UTF-8 in find — regexp rejects those bytes — so compile defensively and
	// leave re nil on failure (disabling the fuzzy branch) rather than panicking
	// on an adversarial old_string. The nil guard below already handles it.
	var re *regexp.Regexp
	if words := strings.Fields(strings.TrimSpace(find)); len(words) > 0 {
		parts := make([]string, len(words))
		for i, w := range words {
			parts[i] = regexp.QuoteMeta(w)
		}
		pattern := strings.Join(parts, `\s+`)
		re, _ = regexp.Compile(pattern)
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if norm(line) == normalizedFind {
			out = append(out, line)
			continue
		}
		if re != nil && strings.Contains(norm(line), normalizedFind) {
			if m := re.FindString(line); m != "" {
				out = append(out, m)
			}
		}
	}

	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if norm(block) == normalizedFind {
				out = append(out, block)
			}
		}
	}
	return out
}

// indentationFlexibleReplacer strips common indent before matching.
func indentationFlexibleReplacer(content, find string) []string {
	removeIndent := func(text string) string {
		lines := strings.Split(text, "\n")
		minIndent := -1
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			n := 0
			for n < len(l) && (l[n] == ' ' || l[n] == '\t') {
				n++
			}
			if minIndent == -1 || n < minIndent {
				minIndent = n
			}
		}
		if minIndent <= 0 {
			return text
		}
		out := make([]string, len(lines))
		for i, l := range lines {
			switch {
			case strings.TrimSpace(l) == "":
				out[i] = l
			case len(l) >= minIndent:
				out[i] = l[minIndent:]
			default:
				out[i] = l
			}
		}
		return strings.Join(out, "\n")
	}
	normalizedFind := removeIndent(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	var out []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndent(block) == normalizedFind {
			out = append(out, block)
		}
	}
	return out
}

// escapeNormalizedReplacer treats literal `\n` `\t` `\r` `\\` `\"` `\'` “ ` “ `\$`
// in find as their unescaped forms.
func escapeNormalizedReplacer(content, find string) []string {
	unescape := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] != '\\' || i+1 >= len(s) {
				b.WriteByte(s[i])
				continue
			}
			c := s[i+1]
			switch c {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\'', '"', '`', '\\', '\n', '$':
				b.WriteByte(c)
			default:
				b.WriteByte(s[i])
				b.WriteByte(c)
			}
			i++
		}
		return b.String()
	}
	unescapedFind := unescape(find)
	var out []string
	if strings.Contains(content, unescapedFind) {
		out = append(out, unescapedFind)
	}
	lines := strings.Split(content, "\n")
	findLines := strings.Split(unescapedFind, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if unescape(block) == unescapedFind {
			out = append(out, block)
		}
	}
	return out
}

// trimmedBoundaryReplacer matches with leading/trailing whitespace stripped.
func trimmedBoundaryReplacer(content, find string) []string {
	trimmed := strings.TrimSpace(find)
	if trimmed == find {
		return nil
	}
	var out []string
	if strings.Contains(content, trimmed) {
		out = append(out, trimmed)
	}
	lines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmed {
			out = append(out, block)
		}
	}
	return out
}

// contextAwareReplacer matches blocks where >= 50% of middle lines match.
func contextAwareReplacer(content, find string) []string {
	findLines := strings.Split(find, "\n")
	if len(findLines) < 3 {
		return nil
	}
	if n := len(findLines); findLines[n-1] == "" {
		findLines = findLines[:n-1]
	}
	if len(findLines) < 3 {
		return nil
	}
	contentLines := strings.Split(content, "\n")
	first := strings.TrimSpace(findLines[0])
	last := strings.TrimSpace(findLines[len(findLines)-1])
	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != first {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) != last {
				continue
			}
			block := contentLines[i : j+1]
			if len(block) != len(findLines) {
				break
			}
			matching, totalNonEmpty := 0, 0
			for k := 1; k < len(block)-1; k++ {
				bl := strings.TrimSpace(block[k])
				fl := strings.TrimSpace(findLines[k])
				if len(bl) > 0 || len(fl) > 0 {
					totalNonEmpty++
					if bl == fl {
						matching++
					}
				}
			}
			if totalNonEmpty == 0 || float64(matching)/float64(totalNonEmpty) >= 0.5 {
				return []string{strings.Join(block, "\n")}
			}
			break
		}
	}
	return nil
}

// multiOccurrenceReplacer yields find for every exact match position so the
// driver loop can decide based on replaceAll.
func multiOccurrenceReplacer(content, find string) []string {
	if find == "" {
		return nil
	}
	var out []string
	start := 0
	for {
		idx := strings.Index(content[start:], find)
		if idx == -1 {
			return out
		}
		out = append(out, find)
		start += idx + len(find)
	}
}

// levenshtein returns the edit distance between a and b. Used by
// blockAnchorReplacer to score middle-line similarity.
func levenshtein(a, b string) int {
	if len(a) == 0 || len(b) == 0 {
		return max(len(a), len(b))
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(min(prev[j]+1, curr[j-1]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
