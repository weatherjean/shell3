package kit

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// FileName is the kit a config directory is read from. Its presence is what
// makes a directory a shell3 config — there is no second format. It lives
// here because this package defines the format; config and agentsetup used to
// carry a copy each.
const FileName = "shell3.sh"

// Tool is one declared verb: what the model sees, plus the shell function that
// implements it. Params reach the function body as environment variables.
type Tool struct {
	Name, Desc, Func string
	Params           map[string]Param
	Line             int
}

// Skill is knowledge an agent reads; Func emits the body on stdout.
type Skill struct {
	Name, Func string
	// Body is the skill's prose, read statically from its heredoc at parse
	// time — a kit is never executed to learn what it says.
	Body string
	Line int
}

// Test is one declared tool test; Func runs it under the test harness.
type Test struct {
	Name, Func string
	Line       int
}

// Agent is one declared agent: prompt, capability list, and everything scoped
// under it.
type Agent struct {
	Name, Desc, Model, Workdir, PromptFunc string
	// Prompt is the system prompt, read statically from the prompt function's
	// heredoc.
	Prompt string
	Use    []string
	// Context lists config-dir-relative files re-read at every turn start and
	// appended to the prompt — an agent's live memory.
	Context []string
	// MCP is the `mcp:` opt-in: the wiring's mcp server names whose tools this
	// agent gets. MCPAll is the `mcp: all` form. Both empty/false (the
	// default) means no MCP tools.
	MCP    []string
	MCPAll bool
	Tools  []Tool
	Skills []Skill
	Tests  []Test
	Line   int
}

// Group is a shared bundle of tools and skills that agents import via `use:`.
type Group struct {
	Name   string
	Tools  []Tool
	Skills []Skill
	Line   int
}

// Kit is a parsed kit file.
type Kit struct {
	Wiring map[string]any
	Agents []Agent
	Shared []Group
	// Gates and Notes map an agent name to the shell function governing its
	// tool calls (before) and tool results (after). Both are declared in the
	// kit rather than as hooks/*.sh files, so an install is one file.
	Gates map[string]string
	Notes map[string]string
}

// Parse turns kit source into typed declarations. It never executes the file.
func Parse(src []byte) (*Kit, error) {
	srcLines := strings.Split(string(src), "\n")
	blocks, err := scanBlocks(src)
	if err != nil {
		return nil, err
	}
	funcs, err := scanFuncs(src)
	if err != nil {
		return nil, err
	}

	seenFunc := map[string]int{}
	for _, f := range funcs {
		if prev, dup := seenFunc[f.name]; dup {
			return nil, fmt.Errorf("line %d: function %q is already defined at line %d", f.line, f.name, prev)
		}
		seenFunc[f.name] = f.line
	}

	// nextBlockLine is the ceiling for binding: a declaration owns the function
	// under it only if that function appears before the next declaration.
	// Without it, an agent block whose prompt function is missing silently binds
	// the following tool's implementation instead.
	nextBlockLine := func(n int) int {
		for _, b := range blocks {
			if b.line > n {
				return b.line
			}
		}
		return 1 << 30
	}
	nextFunc := func(n int) (fnDef, bool) {
		ceiling := nextBlockLine(n)
		for _, f := range funcs {
			if f.line > n {
				if f.line > ceiling {
					return fnDef{}, false
				}
				return f, true
			}
		}
		return fnDef{}, false
	}

	k := &Kit{}
	seenName := map[string]int{}
	var curAgent *Agent
	var curGroup *Group

	for _, b := range blocks {
		d, err := decodeBlock(b)
		if err != nil {
			return nil, err
		}

		switch d.kind {
		case declWiring:
			if k.Wiring != nil {
				return nil, fmt.Errorf("line %d: a second shell3: wiring block", d.line)
			}
			k.Wiring = d.wiring

		case declAgent, declShared:
			key := d.kind.String() + ":" + d.name
			if prev, dup := seenName[key]; dup {
				return nil, fmt.Errorf("line %d: %s %q is already declared at line %d", d.line, d.kind, d.name, prev)
			}
			seenName[key] = d.line

			if d.kind == declAgent {
				f, ok := nextFunc(d.endLine)
				if !ok {
					return nil, fmt.Errorf("line %d: agent %q has no prompt function under it", d.line, d.name)
				}
				prompt, perr := extractHeredoc(srcLines, f.line, "agent", d.name)
				if perr != nil {
					return nil, fmt.Errorf("line %d: %w", d.line, perr)
				}
				k.Agents = append(k.Agents, Agent{
					Name: d.name, Desc: d.desc, Model: d.model, Workdir: d.workdir,
					Use: d.use, Context: d.context, MCP: d.mcp, MCPAll: d.mcpAll,
					PromptFunc: f.name, Prompt: prompt, Line: d.line,
				})
				curAgent, curGroup = &k.Agents[len(k.Agents)-1], nil
			} else {
				k.Shared = append(k.Shared, Group{Name: d.name, Line: d.line})
				curGroup, curAgent = &k.Shared[len(k.Shared)-1], nil
			}

		case declGate, declNote:
			// Gates and notes are NOT positional: they name the agents they
			// govern, because one function usually governs several (a subagent
			// with no gate of its own runs ungated, and copying the rules is
			// how they drift apart). They may appear anywhere in the file.
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: %s %q has no function under it", d.line, d.kind, d.name)
			}
			target := &k.Gates
			if d.kind == declNote {
				target = &k.Notes
			}
			if *target == nil {
				*target = map[string]string{}
			}
			for _, agent := range d.agents {
				if _, dup := (*target)[agent]; dup {
					return nil, fmt.Errorf("line %d: a second %s for agent %q — one function governs an agent, name several agents on one block instead", d.line, d.kind, agent)
				}
				(*target)[agent] = f.name
			}

		case declTool, declSkill, declTest:
			if curAgent == nil && curGroup == nil {
				return nil, fmt.Errorf("line %d: %s %q appears before any agent or shared block", d.line, d.kind, d.name)
			}
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: %s %q has no function under it", d.line, d.kind, d.name)
			}
			if err := attach(curAgent, curGroup, d, f, srcLines); err != nil {
				return nil, err
			}
		}
	}

	// Checked after the loop, not inside it: a gate may be declared above the
	// agent it governs, and a typo'd name must not silently mean "ungated".
	known := map[string]bool{}
	for _, a := range k.Agents {
		known[a.Name] = true
	}
	// Ordered, not a map range: when both a gate and a note name an unknown
	// agent, the error text must be the same on every run.
	for _, decl := range []struct {
		kind string
		m    map[string]string
	}{{"gate", k.Gates}, {"note", k.Notes}} {
		for _, agent := range slices.Sorted(maps.Keys(decl.m)) {
			if !known[agent] {
				return nil, fmt.Errorf("%s names agent %q, which this kit does not declare", decl.kind, agent)
			}
		}
	}
	return k, nil
}

// attach files a tool/skill/test onto whichever scope is open.
func attach(a *Agent, g *Group, d decl, f fnDef, srcLines []string) error {
	switch d.kind {
	case declTool:
		if d.desc == "" {
			return fmt.Errorf("line %d: tool %q needs a description", d.line, d.name)
		}
		for pname, p := range d.params {
			if _, ok := jsonType[p.Type]; !ok {
				return fmt.Errorf("line %d: tool %q param %q has type %q — want string, int, or bool", d.line, d.name, pname, p.Type)
			}
			// A param becomes an environment variable in the tool's shell, so it
			// must be a valid identifier and must not shadow the environment the
			// body relies on to find programs.
			if !paramName.MatchString(pname) {
				return fmt.Errorf("line %d: tool %q param %q is not a valid identifier — params become environment variables", d.line, d.name, pname)
			}
			for _, reserved := range passthroughEnv {
				if pname == reserved {
					return fmt.Errorf("line %d: tool %q param %q shadows the %s environment variable", d.line, d.name, pname, reserved)
				}
			}
		}
		t := Tool{Name: d.name, Desc: d.desc, Func: f.name, Params: d.params, Line: d.line}
		existing := a.tools(g)
		for _, prev := range existing {
			if prev.Name == t.Name {
				return fmt.Errorf("line %d: tool %q is already declared in this scope at line %d", d.line, t.Name, prev.Line)
			}
		}
		if a != nil {
			a.Tools = append(a.Tools, t)
		} else {
			g.Tools = append(g.Tools, t)
		}

	case declSkill:
		body, err := extractHeredoc(srcLines, f.line, "skill", d.name)
		if err != nil {
			return fmt.Errorf("line %d: %w", d.line, err)
		}
		s := Skill{Name: d.name, Func: f.name, Body: body, Line: d.line}
		if a != nil {
			a.Skills = append(a.Skills, s)
		} else {
			g.Skills = append(g.Skills, s)
		}

	case declTest:
		if a == nil {
			return fmt.Errorf("line %d: test %q must sit under an agent", d.line, d.name)
		}
		a.Tests = append(a.Tests, Test{Name: d.name, Func: f.name, Line: d.line})
	}
	return nil
}

// tools returns whichever open scope's tool list applies (a may be nil).
func (a *Agent) tools(g *Group) []Tool {
	if a != nil {
		return a.Tools
	}
	return g.Tools
}
