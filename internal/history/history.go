// Package history reads and writes conversation history as markdown files.
package history

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Load reads a markdown history file. Returns nil slice if file doesn't exist.
func Load(path string) ([]llm.Message, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: read %s: %w", path, err)
	}
	return parse(string(data)), nil
}

// Save writes msgs to a markdown file, overwriting any existing content.
func Save(path string, msgs []llm.Message) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Session: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	for _, m := range msgs {
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", roleLabel(m.Role), m.Content)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("history: write %s: %w", path, err)
	}
	return nil
}

func parse(content string) []llm.Message {
	var msgs []llm.Message
	sections := strings.Split(content, "\n## ")
	for _, sec := range sections[1:] {
		lines := strings.SplitN(sec, "\n", 3)
		if len(lines) < 3 {
			continue
		}
		roleStr := strings.TrimSpace(lines[0])
		body := strings.TrimSpace(lines[2])
		role := labelToRole(roleStr)
		if role == "" {
			continue
		}
		msgs = append(msgs, llm.Message{Role: role, Content: body})
	}
	return msgs
}

func roleLabel(r llm.Role) string {
	switch r {
	case llm.RoleUser:
		return "User"
	case llm.RoleAssistant:
		return "Assistant"
	default:
		return string(r)
	}
}

func labelToRole(s string) llm.Role {
	switch strings.ToLower(s) {
	case "user":
		return llm.RoleUser
	case "assistant":
		return llm.RoleAssistant
	}
	return ""
}
