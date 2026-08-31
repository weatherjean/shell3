package kit

import (
	"fmt"
	"strings"
)

// extractHeredoc returns the text of the first heredoc inside the function
// whose header is at line fnLine (1-indexed). Prompts and skills are prose, so
// they are written as `name() { cat <<'EOF' … EOF }` and their text is read
// STATICALLY — the parser never executes a kit to learn what it says.
func extractHeredoc(lines []string, fnLine int, what, name string) (string, error) {
	i := fnLine - 1
	if i < 0 || i >= len(lines) {
		return "", fmt.Errorf("%s %q: function line %d out of range", what, name, fnLine)
	}

	var braces braceScanner
	depth, _ := braces.advance(lines[i], 0)
	for ; i < len(lines); i++ {
		if delim, ok := braces.heredocDelimiter(lines[i]); ok {
			var body []string
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == delim {
					return strings.Join(body, "\n"), nil
				}
				body = append(body, lines[j])
			}
			return "", fmt.Errorf("%s %q: heredoc %q is never closed", what, name, delim)
		}
		if i > fnLine-1 {
			depth, _ = braces.advance(lines[i], depth)
			if depth <= 0 {
				break
			}
		}
	}
	return "", fmt.Errorf("%s %q: no heredoc — prose belongs in a `cat <<'EOF'` block so it can be read without running the kit", what, name)
}
