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
			result = append(result, s)
		}
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

// BuildSection formats skills into a # Skills section for the system prompt.
// Only skills with a name (i.e. valid frontmatter) are included.
// Full skill body is NOT injected — model reads the file on demand via bash.
func BuildSection(ss []Skill) string {
	if len(ss) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n# Skills\n\n")
	sb.WriteString("Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.\n\n")
	for _, s := range ss {
		fmt.Fprintf(&sb, "## %s\n", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&sb, "%s\n", s.Description)
		}
		fmt.Fprintf(&sb, "File: %s\n\n", s.Path)
	}
	return sb.String()
}
