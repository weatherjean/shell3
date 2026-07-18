package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// scanSkillDir reads the skills/ directory: every flat *.md that parses
// becomes a Skill (filename order). An invalid file — empty, no frontmatter,
// no description, empty body — is skipped with a warning so a stray .md never
// takes the bot down (`shell3 health` hardens those warnings into failures).
// A missing dir yields no skills: presence of the dir is what enables the
// feature. A later file reusing an already-taken name is skipped with a
// warning: the ## Skills index must never carry two entries for one name.
func scanSkillDir(abs string, warn func(string)) ([]Skill, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var skills []Skill
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(abs, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		s, err := parseSkillFile(data, e.Name())
		if err != nil {
			warn(fmt.Sprintf("skill file %q skipped: %v", path, err))
			continue
		}
		if seen[s.Name] {
			warn(fmt.Sprintf("skill file %q skipped: duplicate skill name %q", path, s.Name))
			continue
		}
		seen[s.Name] = true
		s.Path = path
		skills = append(skills, s)
	}
	return skills, nil
}

// parseSkillFile extracts a Skill from a markdown skill file: frontmatter with
// a required `description` and an optional `name` (defaulting to the filename
// sans .md), followed by a non-empty body. Unknown frontmatter keys are
// ignored. Path is left for the caller to fill in.
func parseSkillFile(data []byte, filename string) (Skill, error) {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter: %v", err)
	}
	if fm.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	if fm.Name == "" {
		fm.Name = strings.TrimSuffix(filename, ".md")
	}
	if strings.TrimSpace(body) == "" {
		return Skill{}, fmt.Errorf("no body after frontmatter")
	}
	return Skill{Name: fm.Name, Description: fm.Description}, nil
}
