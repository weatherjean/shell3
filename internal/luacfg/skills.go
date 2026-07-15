package luacfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// skillScanner resolves agents' skills dirs, caching each directory scan so
// declarers sharing a dir don't re-read files — and each invalid file warns
// once per load, not once per declarer.
type skillScanner struct {
	cfgDir string
	warn   func(string)
	cache  map[string]dirScan
}

type dirScan struct {
	skills []Skill
	err    error
}

func newSkillScanner(cfgDir string, warn func(string)) *skillScanner {
	return &skillScanner{cfgDir: cfgDir, warn: warn, cache: map[string]dirScan{}}
}

// resolve returns one declarer's skills in dir-then-filename order. Relative
// dirs — including ../ — resolve against the config dir; a missing or
// unreadable dir fails the load (it's a config typo; ctx labels the declarer).
// A later file reusing an already-taken name is skipped with a warning: the
// ## Skills index must never carry two entries for one name.
func (sc *skillScanner) resolve(dirs []string, ctx string) ([]Skill, error) {
	var out []Skill
	seen := map[string]bool{}
	for _, d := range dirs {
		abs := d
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(sc.cfgDir, abs)
		}
		skills, err := sc.scan(abs)
		if err != nil {
			return nil, fmt.Errorf("config: %s: skills dir %q: %w", ctx, d, err)
		}
		for _, s := range skills {
			if seen[s.Name] {
				sc.warn(fmt.Sprintf("%s: skill file %q skipped: duplicate skill name %q", ctx, s.Path, s.Name))
				continue
			}
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// scan reads one directory (cached): every flat *.md that parses becomes a
// Skill. An invalid file — empty, no frontmatter, no description, empty body —
// is skipped with a warning so a stray .md never takes the bot down
// (`shell3 health` hardens those warnings into errors).
func (sc *skillScanner) scan(abs string) ([]Skill, error) {
	if r, ok := sc.cache[abs]; ok {
		return r.skills, r.err
	}
	skills, err := scanSkillDir(abs, sc.warn)
	sc.cache[abs] = dirScan{skills, err}
	return skills, err
}

func scanSkillDir(abs string, warn func(string)) ([]Skill, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var skills []Skill
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
		s.Path = path
		skills = append(skills, s)
	}
	return skills, nil
}

// parseSkillFile extracts a Skill from a markdown skill file: a YAML
// frontmatter block (--- ... ---) with a required `description` and an
// optional `name` (defaulting to the filename sans .md), followed by a
// non-empty body. Unknown frontmatter keys are ignored. Path is left for the
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
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter: %v", err)
	}
	if fm.Description == "" {
		return Skill{}, fmt.Errorf("frontmatter has no description")
	}
	if fm.Name == "" {
		fm.Name = strings.TrimSuffix(filename, ".md")
	}
	if strings.TrimSpace(strings.Join(lines[end+1:], "\n")) == "" {
		return Skill{}, fmt.Errorf("no body after frontmatter")
	}
	return Skill{Name: fm.Name, Description: fm.Description}, nil
}
