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

// heredocStart captures the delimiter of a heredoc opened on a line:
// <<EOF, <<'EOF', <<"EOF", <<-EOF.
var heredocStart = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

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
	depth := braceDelta(header)
	if hd := heredocStart.FindStringSubmatch(header); hd != nil {
		if err := skipHeredoc(lines, i, hd[1], name, start); err != nil {
			return 0, err
		}
	}
	for *i++; *i < len(lines) && depth > 0; *i++ {
		line := lines[*i]
		if hd := heredocStart.FindStringSubmatch(line); hd != nil {
			if err := skipHeredoc(lines, i, hd[1], name, start); err != nil {
				return 0, err
			}
			continue
		}
		depth += braceDelta(line)
	}
	*i--
	return depth, nil
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

// braceDelta is the net brace change contributed by one line.
func braceDelta(s string) int {
	return strings.Count(s, "{") - strings.Count(s, "}")
}
