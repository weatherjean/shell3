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

// Command is one declared host command: a verb the front-end answers by
// running Func, with no model turn and no tokens spent. Commands are
// install-wide rather than scoped to an agent.
type Command struct {
	Name, Desc, Func string
	Line             int
}

// EventHook is one declared event subscriber: Func receives the JSON for each
// event whose kind is named in On. It observes only — its stdout is ignored,
// and it can neither refuse nor rewrite anything.
type EventHook struct {
	Func string
	On   []string
	Line int
}

// EventNames are the event kinds an `event:` block may subscribe to. They
// mirror chat.EventKind.String(); internal/chat's kit_events_test.go pins the
// two lists together, which is why this one can live here without kit
// importing the runtime.
var EventNames = []string{
	"session_end",
	"user_message",
	"assistant_message",
	"assistant_token",
	"assistant_reasoning",
	"tool_call",
	"tool_result",
	"error",
	"usage",
	"turn_done",
	"system_reminder",
	"retry",
	"compacted",
}

// ReservedCommands are the verbs a front-end answers itself. A `command:`
// block may not declare one: the built-in is matched first at dispatch, so
// the declaration would never fire and its author would have no way to see
// why. Mirrors telegram.BotCommands(); internal/telegram's
// kitcommands_test.go pins the two lists together, which is why this one can
// live here without kit importing a front-end.
var ReservedCommands = []string{
	"dash",
	"stop",
	"superstop",
	"new",
	"run",
	"btw",
	"reload",
	"quiet",
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
	// Events maps an agent name to the subscriber observing its event stream.
	Events map[string]EventHook
	// Commands maps a command name to its declaration. Keyed by command
	// rather than by agent: a host command belongs to the install.
	Commands map[string]Command
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

		case declCommand:
			// Positional like tool:, because a command names itself. It is
			// deliberately NOT scoped to the open agent — a host command is
			// answered by the front-end, not by a model, so there is no agent
			// for it to belong to.
			if d.desc == "" {
				return nil, fmt.Errorf("line %d: command %q needs a description — it is registered in the front-end's command menu", d.line, d.name)
			}
			if slices.Contains(ReservedCommands, d.name) {
				return nil, fmt.Errorf("line %d: command %q is a built-in — pick another name (built-ins are matched first, so this one would never fire)", d.line, d.name)
			}
			if prev, dup := k.Commands[d.name]; dup {
				return nil, fmt.Errorf("line %d: command %q is already declared at line %d", d.line, d.name, prev.Line)
			}
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: command %q has no function under it", d.line, d.name)
			}
			if k.Commands == nil {
				k.Commands = map[string]Command{}
			}
			k.Commands[d.name] = Command{Name: d.name, Desc: d.desc, Func: f.name, Line: d.line}

		case declGate, declNote, declEvent:
			// Gates and notes are NOT positional: they name the agents they
			// govern, because one function usually governs several (a subagent
			// with no gate of its own runs ungated, and copying the rules is
			// how they drift apart). They may appear anywhere in the file.
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: %s %q has no function under it", d.line, d.kind, d.name)
			}
			if d.kind == declEvent {
				// on: is mandatory. assistant_token fires once per streamed
				// token, so an unfiltered subscriber would fork a shell
				// thousands of times per turn — the filter is what makes this
				// hook affordable, not a convenience.
				if len(d.on) == 0 {
					return nil, fmt.Errorf("line %d: event %q needs an on: list naming the event kinds it receives (one of %s)", d.line, d.name, strings.Join(EventNames, ", "))
				}
				for _, name := range d.on {
					if !slices.Contains(EventNames, name) {
						return nil, fmt.Errorf("line %d: event %q subscribes to unknown kind %q — want one of %s", d.line, d.name, name, strings.Join(EventNames, ", "))
					}
				}
				if k.Events == nil {
					k.Events = map[string]EventHook{}
				}
				for _, agent := range d.agents {
					if _, dup := k.Events[agent]; dup {
						return nil, fmt.Errorf("line %d: a second event for agent %q — one function observes an agent, name several agents on one block instead", d.line, agent)
					}
					k.Events[agent] = EventHook{Func: f.name, On: d.on, Line: d.line}
				}
				break
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
	for _, agent := range slices.Sorted(maps.Keys(k.Events)) {
		if !known[agent] {
			return nil, fmt.Errorf("event names agent %q, which this kit does not declare", agent)
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
