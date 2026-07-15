package luacfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveSkillDirs scans each declared skills directory (relative paths —
// including ../ — resolve against cfgDir) for flat *.md skill files and
// returns the resolved skills in dir-then-filename order. A missing or
// unreadable directory fails the load (it's a config typo); an invalid skill
// file — empty, no frontmatter, no description, empty body — is skipped with a
// warning so a stray .md never takes the bot down (`shell3 health` surfaces
// those warnings as errors). A later file reusing an already-taken name is
// skipped too: the ## Skills index must never carry two entries for one name.
// ctx labels errors/warnings with the declaring agent.
func resolveSkillDirs(cfgDir string, dirs []string, ctx string, warn func(string)) ([]Skill, error) {
	var out []Skill
	seen := map[string]bool{}
	for _, d := range dirs {
		abs := d
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cfgDir, abs)
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, fmt.Errorf("config: %s: skills dir %q: %w", ctx, d, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(abs, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("config: %s: skill file %q: %w", ctx, path, err)
			}
			s, err := parseSkillFile(data, e.Name())
			if err != nil {
				warn(fmt.Sprintf("%s: skill file %q skipped: %v", ctx, path, err))
				continue
			}
			if seen[s.Name] {
				warn(fmt.Sprintf("%s: skill file %q skipped: duplicate skill name %q", ctx, path, s.Name))
				continue
			}
			seen[s.Name] = true
			s.Path = path
			out = append(out, s)
		}
	}
	return out, nil
}

// parseSkillFile extracts a Skill from a markdown skill file: a YAML
// frontmatter block (--- ... ---) with a required `description` and an
// optional `name` (defaulting to the filename sans .md), followed by a
// non-empty body. Only those two keys are read; anything else in the
// frontmatter is ignored for forward compatibility. Path is left for the
// caller to fill in.
func parseSkillFile(data []byte, filename string) (Skill, error) {
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return Skill{}, fmt.Errorf("file is empty")
	}
	lines := strings.Split(text, "\n")
	if strings.TrimRight(lines[0], "\r") != "---" {
		return Skill{}, fmt.Errorf("missing frontmatter (file must start with ---)")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return Skill{}, fmt.Errorf("unterminated frontmatter (no closing ---)")
	}
	s := Skill{Name: strings.TrimSuffix(filename, ".md")}
	for _, line := range lines[1:end] {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			if v := unquote(val); v != "" {
				s.Name = v
			}
		case "description":
			s.Description = unquote(val)
		}
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	if strings.TrimSpace(strings.Join(lines[end+1:], "\n")) == "" {
		return Skill{}, fmt.Errorf("no body after frontmatter")
	}
	return s, nil
}

// unquote trims whitespace and one matching pair of single or double quotes —
// the two quoting styles YAML frontmatter authors actually use for flat
// string values.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v
}
