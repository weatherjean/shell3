package kit

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// Builtins are the tool names an agent may name in `use:` to get a built-in
// tool.
var Builtins = []string{"bash", "bash_bg", "edit", "history"}

// removedBuiltins are names a kit may still declare from an older install.
// Each fails the load naming its replacement: a stale kit must say what to do
// instead, never arm nothing silently.
var removedBuiltins = map[string]string{
	"media": "the read_media tool was removed. Perception is a tool you declare: " +
		"see the using-llms skill for a `see` tool block, and ask the operator " +
		"which vision model to point it at",
}

// Resolved is one agent's fully-resolved capability set: the built-ins it asked
// for, the MCP servers it opted into, and every declared tool it can call
// (its own plus those from the shared groups it imports). Skills are FILES
// (skills/*.md, scanned by config), never part of the kit.
type Resolved struct {
	Agent    Agent
	Builtins []string
	Tools    []Tool
}

// Resolve turns an agent's `use:` list into concrete capabilities. Entries
// resolve as a built-in name or a shared group declared in the kit. An entry
// matching neither is an error — a typo in `use:` must not silently mean "no
// capability". MCP opt-ins use Agent.MCP instead.
//
// isMain relaxes the rule for the agent you talk to: it gets every built-in
// without naming one, because you are steering it in real time. An employee
// runs unattended, so its surface is exactly what someone declared.
func (k *Kit) Resolve(a Agent, isMain bool) (Resolved, error) {
	r := Resolved{Agent: a, Tools: append([]Tool{}, a.Tools...)}
	if isMain {
		r.Builtins = append(r.Builtins, Builtins...)
	}

	for _, name := range a.Use {
		switch {
		case strings.HasPrefix(name, "mcp:"):
			return Resolved{}, fmt.Errorf("agent %q (line %d): use: %q — MCP opt-ins belong in the agent block's mcp: list", a.Name, a.Line, name)
		case isBuiltin(name):
			if !isMain {
				r.Builtins = append(r.Builtins, name)
			}
			continue
		}
		if why, gone := removedBuiltins[name]; gone {
			return Resolved{}, fmt.Errorf("agent %q (line %d): use: %q — %s", a.Name, a.Line, name, why)
		}
		g, ok := k.group(name)
		if !ok {
			return Resolved{}, fmt.Errorf("agent %q (line %d): use: %q is not a built-in, an mcp: server, or a shared group in this kit", a.Name, a.Line, name)
		}
		r.Tools = append(r.Tools, g.Tools...)
	}

	seen := map[string]int{}
	for _, t := range r.Tools {
		if prev, dup := seen[t.Name]; dup {
			return Resolved{}, fmt.Errorf("agent %q: tool %q reaches it twice (lines %d and %d) — a shared group collides with a local tool", a.Name, t.Name, prev, t.Line)
		}
		seen[t.Name] = t.Line
	}
	return r, nil
}

func isBuiltin(name string) bool { return slices.Contains(Builtins, name) }

func (k *Kit) group(name string) (Group, bool) {
	for _, g := range k.Shared {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}

// ToolDefs renders every declared tool this agent can call as the schema the
// model receives.
func (r Resolved) ToolDefs() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.Tools))
	for _, t := range r.Tools {
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name,
			Description: t.Desc,
			Parameters:  t.Schema(),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// ToolByName finds a declared tool this agent can call.
func (r Resolved) ToolByName(name string) (Tool, bool) {
	for _, t := range r.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// ToolByName finds a declared tool anywhere in the kit for the kit-authoring
// CLI. It returns the first match: agents in declaration order, then shared
// groups. Runtime calls use Resolved.ToolByName and remain agent-scoped.
func (k *Kit) ToolByName(name string) (Tool, bool) {
	for _, a := range k.Agents {
		for _, t := range a.Tools {
			if t.Name == name {
				return t, true
			}
		}
	}
	for _, g := range k.Shared {
		for _, t := range g.Tools {
			if t.Name == name {
				return t, true
			}
		}
	}
	return Tool{}, false
}
