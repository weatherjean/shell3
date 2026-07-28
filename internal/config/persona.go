package config

import (
	"fmt"
	"strings"
)

// BuildPersonaFor renders the final system prompt for the given agent: the
// verbatim agent prompt followed by the engine-injected skills block (when
// the agent's skills dirs yielded any skills). The agent is passed in so
// concurrent sessions with different active agents can render without
// touching global state.
func (c *LoadedConfig) BuildPersonaFor(a Agent) string {
	var b strings.Builder
	b.WriteString(a.Prompt)
	if len(a.Skills) > 0 {
		b.WriteString("\n## Skills\nRead a skill's file with `cat` when it applies.\n")
		for _, s := range a.Skills {
			fmt.Fprintf(&b, "- %s (%s): %s\n", s.Name, s.Path, s.Description)
		}
	}
	// The Chain of Command portfolio brief (projects.md) is appended verbatim
	// to the very end. Only the main agent ever carries one.
	if a.ProjectsBrief != "" {
		b.WriteString("\n" + a.ProjectsBrief + "\n")
	}
	// The `context:` files are resolved fresh here — BuildPersonaFor runs at
	// session-config build, so each new fresh turn re-reads the current file
	// (the agent edits memory.md in one thread and the next message sees it).
	// One `### <config-dir-relative path>` sub-section per file so the agent
	// knows where to edit_file its own brain. Glob-pattern errors were already
	// rejected at load, so the error here is safely ignored.
	if len(a.Context) > 0 {
		if files, err := ResolveContextFiles(c.dir, a.Context); err == nil && len(files) > 0 {
			b.WriteString("\n## Context\n")
			for _, f := range files {
				fmt.Fprintf(&b, "\n### %s\n%s\n", f.Path, f.Body)
			}
		}
	}
	return b.String()
}
