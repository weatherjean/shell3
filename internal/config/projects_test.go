package config

import (
	"strings"
	"testing"
)

// projectFiles builds the two required files for a project named `name` whose
// manager works in `workdir`, plus any extra files (relative to the config
// dir) merged in.
func projectFiles(name, workdir string, extra map[string]string) map[string]string {
	files := map[string]string{
		"projects/" + name + "/project.md": "---\ndescription: my " + name + "\nworkdir: " + workdir + "\n---\nBrief.\n",
		"projects/" + name + "/manager.md": "---\ndescription: manages " + name + "\n---\nYou are the " + name + " manager.\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// TestProjectsLoad: a well-formed project registers its manager as a subagent
// under the project name and exposes the project via Projects().
func TestProjectsLoad(t *testing.T) {
	work := t.TempDir()
	c := mustLoad(t, projectFiles("site", work, nil))

	projs := c.Projects()
	if len(projs) != 1 {
		t.Fatalf("Projects() = %d, want 1", len(projs))
	}
	p := projs[0]
	if p.Name != "site" || p.Description != "my site" || p.Workdir != work || p.Brief != "Brief." {
		t.Fatalf("project = %+v", p)
	}
	if p.Dir == "" || !strings.HasSuffix(p.Dir, "projects/site") {
		t.Fatalf("project Dir = %q", p.Dir)
	}

	sa, ok := c.SubagentByName("site")
	if !ok {
		t.Fatal("manager not registered as subagent under project name")
	}
	if sa.Workdir != work {
		t.Fatalf("manager Workdir = %q, want %q", sa.Workdir, work)
	}
	if !strings.Contains(sa.Prompt, "You are the site manager.") {
		t.Fatalf("manager prompt = %q", sa.Prompt)
	}
	if sa.Description != "manages site" {
		t.Fatalf("manager description = %q", sa.Description)
	}
}

// TestProjectsSkillsManagerOnly: a per-project skill reaches the manager but
// not the main agent.
func TestProjectsSkillsManagerOnly(t *testing.T) {
	work := t.TempDir()
	c := mustLoad(t, projectFiles("site", work, map[string]string{
		"projects/site/skills/deploy.md": "---\ndescription: deploys the site\n---\nRun make deploy.\n",
	}))

	sa, ok := c.SubagentByName("site")
	if !ok {
		t.Fatal("manager missing")
	}
	if len(sa.Skills) != 1 || sa.Skills[0].Name != "deploy" {
		t.Fatalf("manager skills = %+v", sa.Skills)
	}
	if len(c.FirstAgent().Skills) != 0 {
		t.Fatalf("per-project skill leaked to main agent: %+v", c.FirstAgent().Skills)
	}
}

// TestProjectsCollision: a project name that collides with an agents/<name>.md
// subagent is a load error naming both paths.
func TestProjectsCollision(t *testing.T) {
	work := t.TempDir()
	msg := loadErr(t, projectFiles("site", work, map[string]string{
		"agents/site.md": "---\ndescription: a site subagent\ntools: [bash]\n---\nbody\n",
	}))
	if !strings.Contains(msg, "projects/site") || !strings.Contains(msg, "agents/site") {
		t.Fatalf("collision error missing paths: %q", msg)
	}
}

// TestProjectsMissingWorkdir: project.md with no workdir is a load error naming
// the file.
func TestProjectsMissingWorkdir(t *testing.T) {
	msg := loadErr(t, map[string]string{
		"projects/bad/project.md": "---\ndescription: no workdir\n---\nBrief.\n",
		"projects/bad/manager.md": "---\ndescription: manages bad\n---\nbody\n",
	})
	if !strings.Contains(msg, "projects/bad/project.md") || !strings.Contains(msg, "workdir") {
		t.Fatalf("missing-workdir error = %q", msg)
	}
}

// TestProjectsWorkdirNotADir: a workdir pointing at a missing directory is a
// load error.
func TestProjectsWorkdirNotADir(t *testing.T) {
	msg := loadErr(t, map[string]string{
		"projects/bad/project.md": "---\ndescription: bad workdir\nworkdir: /no/such/dir/anywhere\n---\nBrief.\n",
		"projects/bad/manager.md": "---\ndescription: manages bad\n---\nbody\n",
	})
	if !strings.Contains(msg, "projects/bad/project.md") || !strings.Contains(msg, "workdir") {
		t.Fatalf("bad-workdir error = %q", msg)
	}
}

// TestProjectsMissingManager: a project with project.md but no manager.md is a
// load error naming projects/<name>/manager.md.
func TestProjectsMissingManager(t *testing.T) {
	work := t.TempDir()
	msg := loadErr(t, map[string]string{
		"projects/bad/project.md": "---\ndescription: no manager\nworkdir: " + work + "\n---\nBrief.\n",
	})
	if !strings.Contains(msg, "projects/bad/manager.md") {
		t.Fatalf("missing-manager error = %q", msg)
	}
}

// TestProjectsReservedName: a project literally named "agent" is a load error.
func TestProjectsReservedName(t *testing.T) {
	work := t.TempDir()
	msg := loadErr(t, projectFiles("agent", work, nil))
	if !strings.Contains(msg, "projects/agent") || !strings.Contains(msg, "reserved") {
		t.Fatalf("reserved-name error = %q", msg)
	}
}

// TestProjectsBrief: projects.md beside shell3.yaml is carried on the main
// agent and appended to the very end of its rendered persona.
func TestProjectsBrief(t *testing.T) {
	const brief = "## Portfolio\n- site: the marketing site"
	c := mustLoad(t, map[string]string{
		"projects.md": brief + "\n",
	})
	if got := c.FirstAgent().ProjectsBrief; got != brief {
		t.Fatalf("ProjectsBrief = %q, want %q", got, brief)
	}
	persona := c.BuildPersonaFor(c.FirstAgent())
	if !strings.HasSuffix(strings.TrimRight(persona, "\n"), brief) {
		t.Fatalf("persona does not end with brief:\n%q", persona)
	}
}

// TestProjectsBriefAbsent: without projects.md the brief is empty and the
// persona is just the agent prompt.
func TestProjectsBriefAbsent(t *testing.T) {
	c := mustLoad(t, nil)
	if c.FirstAgent().ProjectsBrief != "" {
		t.Fatalf("ProjectsBrief should be empty, got %q", c.FirstAgent().ProjectsBrief)
	}
	if strings.Contains(c.BuildPersonaFor(c.FirstAgent()), "Portfolio") {
		t.Fatal("persona unexpectedly carries a brief")
	}
}
