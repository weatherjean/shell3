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
var Builtins = []string{"bash", "bash_bg", "edit", "media", "history"}

// mainDefaults is what the main agent gets without declaring anything: every
// built-in except `media`. read_media needs a multimodal model, so handing it
// to an agent whose model cannot see images offers a tool that always fails —
// `use: [media]` opts in when the model supports it.
var mainDefaults = []string{"bash", "bash_bg", "edit", "history"}

// Resolved is one agent's fully-resolved capability set: the built-ins it asked
// for, the MCP servers it opted into, and every declared tool it can call
// (its own plus those from the shared groups it imports). Skills are FILES
// (skills/*.md, scanned by config), never part of the kit.
type Resolved struct {
	Agent    Agent
	Builtins []string
	MCP      []string
	Tools    []Tool
}

// Resolve turns an agent's `use:` list into concrete capabilities. Entries
// resolve in order: a built-in name, `mcp:<server>`, then a shared group
// declared in the kit. An entry matching none of those is an error — a typo in
// `use:` must not silently mean "no capability".
//
// isMain relaxes the rule for the agent you talk to: it gets every built-in
// without naming one, because you are steering it in real time. An employee
// runs unattended, so its surface is exactly what someone declared.
func (k *Kit) Resolve(a Agent, isMain bool) (Resolved, error) {
	r := Resolved{Agent: a, Tools: append([]Tool{}, a.Tools...)}
	if isMain {
		r.Builtins = append(r.Builtins, mainDefaults...)
	}

	for _, name := range a.Use {
		switch {
		case strings.HasPrefix(name, "mcp:"):
			r.MCP = append(r.MCP, strings.TrimPrefix(name, "mcp:"))
			continue
		case isBuiltin(name):
			if !isMain || name == "media" {
				r.Builtins = append(r.Builtins, name)
			}
			continue
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

// ToolByName finds a declared tool anywhere in the kit, regardless of which
// agent's scope declares it. Positional scoping bounds what a MODEL may
// call; a cron tool job names no agent at all — it is the operator
// scheduling a tool they declared themselves, so the whole kit is in scope.
// It returns the FIRST match (agents in declaration order, then shared
// groups): when a name is declared in more than one scope, callers that care
// about which one wins must use ToolMatches to detect the ambiguity — this
// method alone cannot tell a unique declaration from an accidental collision.
func (k *Kit) ToolByName(name string) (Tool, bool) {
	matches := k.ToolMatches(name)
	if len(matches) == 0 {
		return Tool{}, false
	}
	return matches[0].Tool, true
}

// ToolMatch is one scope (an agent or a shared group) that declares a tool
// name, returned by ToolMatches.
type ToolMatch struct {
	// Scope names the declaring agent or shared group, prefixed to disambiguate
	// the two kinds of scope in a diagnostic ("agent \"x\"" vs "shared \"x\"").
	Scope string
	Tool  Tool
}

// ToolMatches finds every scope in the kit that declares a tool named name,
// in the same search order ToolByName resolves from (agents in declaration
// order, then shared groups). Duplicate tool names are rejected only WITHIN
// one scope (see kit_test.go), so two agents — or an agent and a shared
// group — may each legally declare the same name; ToolByName then picks
// whichever scope it visits first, silently. A cron `tool:` job names no
// agent, so nothing else disambiguates for it: `shell3 health` calls this to
// fail loudly, naming every declaring scope, rather than let the operator's
// job run whichever function happened to parse first.
func (k *Kit) ToolMatches(name string) []ToolMatch {
	var out []ToolMatch
	for _, a := range k.Agents {
		for _, t := range a.Tools {
			if t.Name == name {
				out = append(out, ToolMatch{Scope: fmt.Sprintf("agent %q", a.Name), Tool: t})
			}
		}
	}
	for _, g := range k.Shared {
		for _, t := range g.Tools {
			if t.Name == name {
				out = append(out, ToolMatch{Scope: fmt.Sprintf("shared %q", g.Name), Tool: t})
			}
		}
	}
	return out
}
