package kit

import (
	"fmt"
	"regexp"
	"strings"
)

// fnDef is one top-level shell function definition.
type fnDef struct {
	name string
	line int
}

// fnHeader matches `name() {` and `name () {`, the only function form a kit may
// use. `function name {` is deliberately not accepted — one form, checked.
var fnHeader = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*\(\)\s*\{`)

// scanFuncs lists top-level function definitions and rejects any other
// top-level statement. Bodies are skipped by brace depth, with heredoc bodies
// skipped wholesale — kit prose lives in heredocs and routinely contains
// unbalanced braces, which would otherwise close the function early and make
// the following prose line look like a top-level statement.
func scanFuncs(src []byte) ([]fnDef, error) {
	lines := strings.Split(string(src), "\n")
	var out []fnDef
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := fnHeader.FindStringSubmatch(t)
		if m == nil {
			return nil, fmt.Errorf("line %d: top-level statement %q — a kit file holds only comments and function definitions", i+1, t)
		}
		start := i + 1
		out = append(out, fnDef{name: m[1], line: start})

		depth, err := walkBody(lines, &i, t, m[1], start)
		if err != nil {
			return nil, err
		}
		if depth > 0 {
			return nil, fmt.Errorf("line %d: function %q is never closed", start, m[1])
		}
	}
	return out, nil
}

// walkBody advances i past the function opened on the header line, returning
// the leftover brace depth (0 when the function closed cleanly).
func walkBody(lines []string, i *int, header, name string, start int) (int, error) {
	var braces braceScanner
	delim, hasHeredoc := braces.heredocDelimiter(header)
	depth, closeAt := braces.advance(header, 0)
	if closeAt >= 0 {
		if err := rejectTrailingStatement(header[closeAt+1:], start, name); err != nil {
			return 0, err
		}
	}
	if hasHeredoc {
		if err := skipHeredoc(lines, i, delim, name, start); err != nil {
			return 0, err
		}
	}
	for *i++; *i < len(lines) && depth > 0; *i++ {
		line := lines[*i]
		delim, hasHeredoc := braces.heredocDelimiter(line)
		if hasHeredoc {
			if err := skipHeredoc(lines, i, delim, name, start); err != nil {
				return 0, err
			}
			continue
		}
		var closeAt int
		depth, closeAt = braces.advance(line, depth)
		if closeAt >= 0 {
			if err := rejectTrailingStatement(line[closeAt+1:], *i+1, name); err != nil {
				return 0, err
			}
		}
	}
	*i--
	return depth, nil
}

func rejectTrailingStatement(tail string, line int, name string) error {
	tail = strings.TrimSpace(tail)
	if strings.HasPrefix(tail, ";") {
		tail = strings.TrimSpace(tail[1:])
	}
	if tail == "" || strings.HasPrefix(tail, "#") {
		return nil
	}
	return fmt.Errorf("line %d: top-level statement %q follows function %q — a kit file holds only comments and function definitions", line, tail, name)
}

// heredocDelimiter returns a real, unquoted << delimiter. A regex matching
// quoted text or comments could skip structural lines and undermine the
// definitions-only check. The kit grammar uses identifier delimiters.
func (s braceScanner) heredocDelimiter(line string) (string, bool) {
	quote := s.quote
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			if quote != '\'' && ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\\':
			i++
		case '\'', '"', '`':
			quote = ch
		case '#':
			if i == 0 || isShellSeparator(line[i-1]) {
				return "", false
			}
		case '<':
			if (i > 0 && line[i-1] == '<') || i+1 >= len(line) || line[i+1] != '<' ||
				(i+2 < len(line) && line[i+2] == '<') {
				continue
			}
			j := i + 2
			if j < len(line) && line[j] == '-' {
				j++
			}
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			var endQuote byte
			if j < len(line) && (line[j] == '\'' || line[j] == '"') {
				endQuote = line[j]
				j++
			}
			start := j
			for j < len(line) && ((line[j] >= 'A' && line[j] <= 'Z') ||
				(line[j] >= 'a' && line[j] <= 'z') || (line[j] >= '0' && line[j] <= '9') || line[j] == '_') {
				j++
			}
			if j == start || (line[start] >= '0' && line[start] <= '9') {
				continue
			}
			if endQuote != 0 && (j >= len(line) || line[j] != endQuote) {
				continue
			}
			return line[start:j], true
		}
	}
	return "", false
}

// skipHeredoc advances i to the heredoc's terminating delimiter line.
func skipHeredoc(lines []string, i *int, delim, fn string, start int) error {
	for *i++; *i < len(lines); *i++ {
		if strings.TrimSpace(lines[*i]) == delim {
			return nil
		}
	}
	return fmt.Errorf("line %d: heredoc %q in function %q is never closed", start, delim, fn)
}

// braceScanner counts structural braces while ignoring quoted text and shell
// comments. Its quote state crosses lines because bash permits multiline
// strings. Counting raw characters let quoted braces disguise a real
// top-level statement as part of the preceding function.
type braceScanner struct {
	quote byte // 0, single quote, double quote, or backtick
}

func (s *braceScanner) advance(line string, depth int) (int, int) {
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if s.quote != 0 {
			if s.quote != '\'' && ch == '\\' {
				i++ // escaped byte inside double quotes or backticks
				continue
			}
			if ch == s.quote {
				s.quote = 0
			}
			continue
		}
		switch ch {
		case '\\':
			i++ // escaped byte cannot open a quote, comment, or brace token
		case '\'', '"', '`':
			s.quote = ch
		case '#':
			if i == 0 || isShellSeparator(line[i-1]) {
				return depth, -1
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return depth, i
			}
		}
	}
	return depth, -1
}

func isShellSeparator(ch byte) bool {
	return strings.ContainsRune(" \t;|&()", rune(ch))
}
