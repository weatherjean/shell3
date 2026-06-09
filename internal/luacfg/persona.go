package luacfg

import (
	"fmt"
	"strings"
)

// BuildPersonaFor renders the final system prompt for the given agent: the
// verbatim agent prompt followed by the engine-injected skills block (when
// skills are active). The agent is passed in so concurrent sessions with
// different active agents can render without touching global state.
func (c *LoadedConfig) BuildPersonaFor(a Agent) string {
	var b strings.Builder
	b.WriteString(a.Prompt)
	if a.SkillsActive() {
		b.WriteString("\n## Skills\nRead a skill body with the `skill` tool when it applies.\n")
		for _, name := range a.Skills {
			for _, s := range c.Skills {
				if s.Name == name {
					fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
				}
			}
		}
	}
	return b.String()
}
