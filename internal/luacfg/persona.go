package luacfg

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/store"
)

type RuntimeData struct {
	Time, CWD, Model string
	CoreMemories     []store.MemoryEntry
}

// BuildPersona renders the final system prompt: the verbatim agent prompt
// followed by engine-injected standard blocks. Replaces text/template.
func (c *LoadedConfig) BuildPersona(rd RuntimeData) string {
	a := c.Active()
	var b strings.Builder
	b.WriteString(a.Prompt)
	if a.Environment {
		fmt.Fprintf(&b, "\n\n## Environment\n- Workdir: %s\n- Model: %s\n- Time: %s\n", rd.CWD, rd.Model, rd.Time)
	}
	if a.CoreMemories && len(rd.CoreMemories) > 0 {
		b.WriteString("\n## Core memories\n")
		for _, m := range rd.CoreMemories {
			fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
		}
	}
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
