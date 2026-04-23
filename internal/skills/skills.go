// Package skills loads markdown skill files and builds system prompt sections.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill holds the parsed content of one skill file.
type Skill struct {
	Name        string
	Description string
	Body        string
}

// LoadAll loads all .md skill files from the given directories.
func LoadAll(dirs []string) ([]Skill, error) {
	var result []Skill
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("skills: read dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("skills: read %s: %w", e.Name(), err)
			}
			s := parse(string(data))
			result = append(result, s)
		}
	}
	return result, nil
}

func parse(content string) Skill {
	if !strings.HasPrefix(content, "---") {
		return Skill{Body: strings.TrimSpace(content)}
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return Skill{Body: strings.TrimSpace(content)}
	}
	fm := parts[1]
	body := strings.TrimSpace(parts[2])

	s := Skill{Body: body}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch k {
		case "name":
			s.Name = v
		case "description":
			s.Description = v
		}
	}
	return s
}

// BuildSection formats skills into a # Skills section for the system prompt.
func BuildSection(ss []Skill) string {
	if len(ss) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n# Skills\n\n")
	for _, s := range ss {
		if s.Name != "" {
			fmt.Fprintf(&sb, "## %s\n", s.Name)
			if s.Description != "" {
				fmt.Fprintf(&sb, "%s\n\n", s.Description)
			}
		}
		sb.WriteString(s.Body)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
