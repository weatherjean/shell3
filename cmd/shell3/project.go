//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/scaffold"
)

type projectFlags struct {
	configDir   string
	workdir     string
	description string
	copySkills  string
}

// newProjectCommand builds the `project` parent and its `new` subcommand — the
// agent-driven scaffolder for a Chain of Command project directory.
func newProjectCommand() *cobra.Command {
	parent := &cobra.Command{
		Use:   "project",
		Short: "Manage Chain of Command projects (projects/<name>/)",
	}

	f := &projectFlags{}
	newCmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a projects/<name>/ project (brief + manager + skills)",
		Long: `Scaffold a projects/<name>/ directory: project.md (the brief), manager.md
(the project's dispatch subagent), and an empty skills/ folder, then append an
index line to projects.md.

This command is designed for the agent to drive from a bash tool call — it
prints created paths and next steps in plain text. Run 'shell3 project new -h';
that help output is the contract the agent reads before invoking it.`,
		Example: `  shell3 project new site --description "marketing site"          # workdir: <config>/.workdirs/site/, created
  shell3 project new api --workdir ~/code/api --copy-skills site  # existing directory (a checkout, a repo)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectNew(cmd.OutOrStdout(), args[0], f)
		},
	}
	newCmd.Flags().StringVar(&f.workdir, "workdir", "", "Directory the project manager's shell runs in (must exist). Omit for the DEFAULT — .workdirs/<name>/ under the config dir, created for you; only pass this when the project must live in a pre-existing directory such as a repo checkout")
	newCmd.Flags().StringVar(&f.description, "description", "", `Short project description (default "<name> project")`)
	newCmd.Flags().StringVar(&f.copySkills, "copy-skills", "", "Copy skills/*.md from an existing project by name")
	addConfigFlag(newCmd, &f.configDir)

	parent.AddCommand(newCmd)
	return parent
}

// runProjectNew resolves the config dir, validates the workdir, scaffolds the
// project tree, appends the projects.md index line, and reports what it made.
func runProjectNew(out io.Writer, name string, f *projectFlags) error {
	dir, err := resolveConfig(f.configDir)
	if err != nil {
		return err
	}

	description := f.description
	if description == "" {
		description = name + " project"
	}

	// The default workdir is <config>/.workdirs/<name>/, created here — one
	// predictable home for project data instead of ad-hoc folders around ~.
	// An explicit --workdir opts out (a repo checkout, an existing tree) and
	// must already exist: this command never creates directories it was
	// merely pointed at.
	var workdir string
	if f.workdir == "" {
		workdir = filepath.Join(dir, ".workdirs", name)
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			return fmt.Errorf("project: creating default workdir %q: %w", workdir, err)
		}
	} else {
		workdir = expandHomeDir(f.workdir)
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			return fmt.Errorf("project: workdir %q is not an existing directory", f.workdir)
		}
	}

	if err := scaffold.RenderProject(dir, scaffold.ProjectValues{
		Name: name, Description: description, Workdir: workdir,
	}, f.copySkills); err != nil {
		return err
	}

	if err := appendProjectIndex(filepath.Join(dir, "projects.md"), name, description); err != nil {
		return err
	}

	pdir := filepath.Join("projects", name)
	fmt.Fprintf(out, "created:\n")
	fmt.Fprintf(out, "  %s\n", filepath.Join(pdir, "project.md"))
	fmt.Fprintf(out, "  %s\n", filepath.Join(pdir, "manager.md"))
	fmt.Fprintf(out, "  %s\n", filepath.Join(pdir, "skills")+"/")
	fmt.Fprintf(out, "  projects.md (index)\n")
	fmt.Fprintf(out, "  workdir: %s\n\n", workdir)
	fmt.Fprintf(out, "project %q created — edit projects/%s/project.md and manager.md to taste.\n", name, name)
	fmt.Fprintf(out, "ls/cat see it immediately; run /reload (or restart) to register the manager for dispatch.\n")
	return nil
}

// appendProjectIndex appends "- **<name>** — <description>" to projects.md,
// creating the file if absent.
func appendProjectIndex(path, name, description string) error {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("project: open projects.md: %w", err)
	}
	defer fh.Close()
	if _, err := fmt.Fprintf(fh, "- **%s** — %s\n", name, description); err != nil {
		return fmt.Errorf("project: append projects.md: %w", err)
	}
	return nil
}

// expandHomeDir resolves a leading ~/ against the user's home directory (mirrors
// the config loader's expansion so a scaffolded workdir validates the same way).
func expandHomeDir(p string) string {
	if len(p) >= 2 && p[0] == '~' && p[1] == '/' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
