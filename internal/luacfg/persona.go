package luacfg

import (
	"fmt"
	"strings"
)

// BuildPersona renders the final system prompt: the verbatim agent prompt
// followed by the engine-injected skills block (when skills are active).
func (c *LoadedConfig) BuildPersona() string {
	a := c.Active()
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
