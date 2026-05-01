// Package skills loads markdown skill files and builds system prompt sections.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill holds metadata parsed from a skill file's frontmatter.
// Body is not loaded — the model reads the file on demand via bash.
type Skill struct {
	Name        string
	Description string
	Path        string // absolute path to the source file
}

// LoadAll loads all .md skill files from dirs in order. Later dirs override
// earlier ones when names collide, so project-local skills (listed last)
// override global skills of the same name.
func LoadAll(dirs []string) ([]Skill, error) {
	byName := make(map[string]Skill)
	var order []string // preserve insertion order for stable output
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
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("skills: read %s: %w", e.Name(), err)
			}
			s := parse(string(data))
			if s.Name == "" {
				continue // frontmatter required; skip skills without it
			}
			s.Path = path
			if _, exists := byName[s.Name]; !exists {
				order = append(order, s.Name)
			}
			byName[s.Name] = s
		}
	}
	result := make([]Skill, 0, len(order))
	for _, name := range order {
		result = append(result, byName[name])
	}
	return result, nil
}

// parse extracts only frontmatter fields — body is never read into memory.
func parse(content string) Skill {
	if !strings.HasPrefix(content, "---") {
		return Skill{}
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return Skill{}
	}
	var s Skill
	for line := range strings.SplitSeq(parts[1], "\n") {
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

// BuildSection formats loaded skills as a list for {{.Skills}} injection.
// Returns name, description, and file path per skill — no header or preamble.
// The persona template is responsible for the # Skills heading and instruction text.
func BuildSection(ss []Skill) string {
	if len(ss) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&sb, "## %s\n", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&sb, "%s\n", s.Description)
		}
		fmt.Fprintf(&sb, "File: %s\n\n", s.Path)
	}
	return strings.TrimRight(sb.String(), "\n")
}
