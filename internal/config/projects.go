package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project is one projects/<name>/ directory: the "brain" of a Chain of
// Command project. Its manager.md registers as a subagent under the project
// name; Workdir is where the manager's shell runs (the real repo, elsewhere
// on disk); Brief is project.md's body.
type Project struct {
	Name        string
	Description string
	Workdir     string
	Brief       string
	Dir         string // absolute projects/<name>/ path
}

// projectFrontmatter is project.md's strict frontmatter.
type projectFrontmatter struct {
	Description string `yaml:"description"`
	Workdir     string `yaml:"workdir"`
}

// loadProjects reads dir/projects/<name>/: project.md (required: description,
// workdir) + manager.md (a subagent parsed exactly like agents/<name>.md,
// named after the project) + optional skills/. Managers are appended to
// c.subagents — the flat namespace agents/ uses — so a name collision with a
// declared subagent (or the reserved "agent") is a load error. Per-project
// skills reach only their manager (the first skill-bearing assistants);
// global skills stay main-agent-only.
func (c *LoadedConfig) loadProjects(dir string, warn func(string)) error {
	root := filepath.Join(dir, "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue // stray files beside project dirs are ignored
		}
		name := e.Name()
		pdir := filepath.Join(root, name)
		label := "projects/" + name
		if name == "agent" {
			return fmt.Errorf("%s: the name \"agent\" is reserved for the main agent — rename the project", label)
		}
		if _, taken := c.SubagentByName(name); taken {
			return fmt.Errorf("%s: name collides with agents/%s.md — project managers and subagents share one namespace", label, name)
		}

		// project.md — description + workdir + brief.
		pdata, err := os.ReadFile(filepath.Join(pdir, "project.md"))
		if err != nil {
			return fmt.Errorf("%s/project.md: %w (every project needs one)", label, err)
		}
		front, body, err := splitFrontmatter(pdata)
		if err != nil {
			return fmt.Errorf("%s/project.md: %w", label, err)
		}
		var fm projectFrontmatter
		dec := yaml.NewDecoder(bytes.NewReader(front))
		dec.KnownFields(true)
		if err := dec.Decode(&fm); err != nil {
			return fmt.Errorf("%s/project.md: frontmatter: %w", label, err)
		}
		if fm.Description == "" {
			return fmt.Errorf("%s/project.md: frontmatter needs a description", label)
		}
		if fm.Workdir == "" {
			return fmt.Errorf("%s/project.md: frontmatter needs a workdir (the repo the manager works in)", label)
		}
		workdir := expandHome(fm.Workdir)
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			return fmt.Errorf("%s/project.md: workdir %q is not an existing directory", label, fm.Workdir)
		}

		// manager.md — the project's dedicated subagent, named after the project.
		mdata, err := os.ReadFile(filepath.Join(pdir, "manager.md"))
		if err != nil {
			return fmt.Errorf("%s/manager.md: %w (every project needs a manager)", label, err)
		}
		sa, err := parseSubagentFile(mdata, name, c.agent.ModelName, label+"/manager.md")
		if err != nil {
			return err
		}
		sa.Workdir = workdir

		// skills/ — per-project, manager-only (same rules as the global dir).
		skills, err := scanSkillDir(filepath.Join(pdir, "skills"), warn)
		if err != nil {
			return fmt.Errorf("%s/skills: %w", label, err)
		}
		sa.Skills = skills

		c.subagents = append(c.subagents, sa)
		c.projects = append(c.projects, Project{
			Name: name, Description: fm.Description, Workdir: workdir,
			Brief: strings.TrimSpace(body), Dir: pdir,
		})
	}
	return nil
}

// expandHome resolves a leading ~/ against the user's home directory.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
