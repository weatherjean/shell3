package config

import (
	"fmt"
	"strings"
)

// splitFrontmatter splits a markdown file into its YAML frontmatter block and
// body: the file must start with a `---` line and contain a closing `---`
// line; everything between is returned as raw YAML, everything after (with
// leading blank lines trimmed) as the body. CRLF line endings are tolerated.
func splitFrontmatter(data []byte) (front []byte, body string, err error) {
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("file is empty")
	}
	lines := strings.Split(text, "\n")
	if strings.TrimRight(lines[0], "\r") != "---" {
		return nil, "", fmt.Errorf("missing frontmatter (file must start with ---)")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("unterminated frontmatter (no closing ---)")
	}
	body = strings.Join(lines[end+1:], "\n")
	body = strings.TrimLeft(body, "\r\n")
	return []byte(strings.Join(lines[1:end], "\n")), body, nil
}
