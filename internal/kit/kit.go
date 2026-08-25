package kit

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	robcron "github.com/robfig/cron/v3"

	"github.com/weatherjean/shell3/internal/notify"
)

// FileName is the kit a config directory is read from; its presence is what
// makes a directory a shell3 config. It lives here because this package
// defines the format.
const FileName = "shell3.sh"

// Tool is one declared verb: what the model sees, plus the shell function that
// implements it. Params reach the function body as environment variables.
type Tool struct {
	Name, Desc, Func string
	Params           map[string]Param
	Line             int
}

// Command is a verb the front-end answers by running Func, with no model turn.
// Commands are install-wide rather than scoped to an agent.
type Command struct {
	Name, Desc, Func string
	Line             int
}

// EventHook receives the JSON for each event whose kind is named in On. It
// observes only: stdout ignored, nothing it can refuse or rewrite.
type EventHook struct {
	Func string
	On   []string
	Line int
}

// EventNames mirror chat.EventKind.String(). internal/chat's
// kit_events_test.go pins the two lists together, which is what lets this one
// live here without kit importing the runtime.
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

// ReservedCommands are the verbs a front-end answers itself. A command: block
// may not declare one — the built-in is matched first, so the declaration
// would never fire and its author could not see why. kitcommands_test.go pins
// this against telegram.BotCommands().
var ReservedCommands = []string{
	"ask",
	"help",
	"dash",
	"stop",
	"superstop",
	"new",
	"run",
	"btw",
	"reload",
	"quiet",
}

// CronJob is one declared scheduled job. Exactly one of Agent (dispatch that
// agent with Prompt) or Tool (run that kit tool directly, with no model turn)
// is set; the tool form binds no function at all, so Func and Prompt are
// empty. Jobs are declaration-ordered; nothing depends on the order.
type CronJob struct {
	Name, Schedule, Agent, Tool string
	// Prompt is the agent job's prompt, read statically from the heredoc in
	// the function under the block — the same way an agent's own prompt is.
	Prompt string
	// Func is where the prompt was read from, kept so an author's error names
	// the definition it points at.
	Func string
	// WorkDir overrides the shell the job runs in.
	WorkDir string
	// Report is what the tick's finish does to the chat (see
	// notify.ReportMode). ReportRaw is the cost valve, since the default
	// wakes the main model to judge every tick. Agent jobs only; a tool job
	// already posts its own result with no agent turn.
	Report notify.ReportMode
	Line   int
}

// Test is one declared tool test; Func runs it under the test harness.
type Test struct {
	Name, Func string
	Line       int
}

// Agent is one declared agent and everything scoped under it.
type Agent struct {
	Name, Desc, Model, Workdir, PromptFunc string
	// Prompt is the system prompt, read statically from the prompt function's
	// heredoc.
	Prompt string
	Use    []string
	// Context lists files re-read at every turn start and appended to the
	// prompt — an agent's live memory.
	Context []string
	// MCP names the servers whose tools this agent gets; MCPAll is `mcp: all`.
	// Neither set means no MCP tools.
	MCP    []string
	MCPAll bool
	Tools  []Tool
	Tests  []Test
	Line   int
}

// Group is a shared bundle of tools that agents import via `use:`.
type Group struct {
	Name  string
	Tools []Tool
	Line  int
}

// Kit is a parsed kit file.
type Kit struct {
	Wiring map[string]any
	Agents []Agent
	Shared []Group
	// Gates and Notes map an agent name to the shell function governing its
	// tool calls (before) and tool results (after).
	Gates map[string]string
	Notes map[string]string
	// Events maps an agent name to the subscriber observing its event stream.
	Events map[string]EventHook
	// Commands is keyed by command, not agent: a host command belongs to the
	// install.
	Commands map[string]Command
	// Crons are the scheduled jobs, in declaration order.
	Crons []CronJob
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

	// nextBlockLine is the binding ceiling: a declaration owns the function
	// under it only if that function precedes the next declaration. Without
	// it, an agent block with no prompt function silently binds the following
	// tool's implementation.
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
			// Positional like tool:, but NOT scoped to the open agent: a
			// command is answered by the front-end, not a model, so there is
			// no agent for it to belong to.
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

		case declCron:
			// Positional in form, scoped to NOTHING: a job names its own
			// target, so it must not open, close or disturb the tool scope.
			job, jerr := cronFromDecl(d, srcLines, nextFunc)
			if jerr != nil {
				return nil, jerr
			}
			for _, prev := range k.Crons {
				if prev.Name == job.Name {
					return nil, fmt.Errorf("line %d: cron %q is already declared at line %d", d.line, job.Name, prev.Line)
				}
			}
			k.Crons = append(k.Crons, job)

		case declGate, declNote, declEvent:
			// Gates and notes are NOT positional: they name the agents they
			// govern, because one function usually governs several and a copy
			// per agent is how two rule sets drift apart.
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: %s %q has no function under it", d.line, d.kind, d.name)
			}
			if d.kind == declEvent {
				// on: is mandatory. assistant_token fires per streamed token,
				// so an unfiltered subscriber would fork a shell thousands of
				// times per turn: the filter is what makes the hook
				// affordable, not a convenience.
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

		case declTool, declTest:
			if curAgent == nil && curGroup == nil {
				return nil, fmt.Errorf("line %d: %s %q appears before any agent or shared block", d.line, d.kind, d.name)
			}
			f, ok := nextFunc(d.endLine)
			if !ok {
				return nil, fmt.Errorf("line %d: %s %q has no function under it", d.line, d.kind, d.name)
			}
			if err := attach(curAgent, curGroup, d, f); err != nil {
				return nil, err
			}
		}
	}

	// After the loop: a gate may be declared above the agent it governs, and
	// a typo'd name must not silently mean "ungated".
	known := map[string]bool{}
	for _, a := range k.Agents {
		known[a.Name] = true
	}
	// Ordered, not a map range, so the error text is stable across runs.
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
	// As with gate targets: a job that cannot fire must fail at load, not at
	// 3am on its first tick, in a log nobody is reading.
	if err := checkCronTargets(k, known); err != nil {
		return nil, err
	}
	return k, nil
}

// cronFromDecl validates one cron: block and reads an agent job's prompt.
func cronFromDecl(d decl, srcLines []string, nextFunc func(int) (fnDef, bool)) (CronJob, error) {
	if d.schedule == "" {
		return CronJob{}, fmt.Errorf("line %d: cron %q needs a schedule", d.line, d.name)
	}
	// The parser cron.New uses at arm time, so a schedule that loads boots.
	if _, err := robcron.ParseStandard(d.schedule); err != nil {
		return CronJob{}, fmt.Errorf("line %d: cron %q has invalid schedule %q: %v", d.line, d.name, d.schedule, err)
	}
	// A job is either a prompt or a tool call, never both and never neither:
	// one with no move at all is a mistake, not a no-op.
	switch {
	case d.cronAgnt != "" && d.cronTool != "":
		return CronJob{}, fmt.Errorf("line %d: cron %q sets both agent: and tool: — exactly one of agent: or tool: (a job is either a prompt or a tool call)", d.line, d.name)
	case d.cronAgnt == "" && d.cronTool == "":
		return CronJob{}, fmt.Errorf("line %d: cron %q needs exactly one of agent: or tool: (a job is either a prompt or a tool call)", d.line, d.name)
	case d.cronTool != "" && d.report != notify.ReportAuto:
		// report: means something only for an agent job. A tool job runs no
		// model turn at all, so there is no report to route.
		return CronJob{}, fmt.Errorf("line %d: cron %q sets both tool: and report: — report only applies to an agent: job (a tool job already posts its own result with no agent turn)", d.line, d.name)
	}
	job := CronJob{
		Name: d.name, Schedule: d.schedule, Agent: d.cronAgnt, Tool: d.cronTool,
		WorkDir: d.workdir, Report: d.report, Line: d.line,
	}
	if job.Tool != "" {
		// A tool job has no prompt and binds NO function: the next definition
		// is somebody else's implementation.
		return job, nil
	}
	f, ok := nextFunc(d.endLine)
	if !ok {
		return CronJob{}, fmt.Errorf("line %d: cron %q has no prompt function under it", d.line, d.name)
	}
	prompt, perr := extractHeredoc(srcLines, f.line, "cron", d.name)
	if perr != nil {
		return CronJob{}, fmt.Errorf("line %d: %w", d.line, perr)
	}
	job.Func, job.Prompt = f.name, prompt
	return job, nil
}

// checkCronTargets resolves every job's target against the whole kit. A tool
// job names no agent, so there is no Resolved set to search: the operator
// scheduling their own tool is the trust boundary, and use: scoping still
// bounds what a MODEL may call.
func checkCronTargets(k *Kit, known map[string]bool) error {
	for _, j := range k.Crons {
		if j.Tool == "" {
			if !known[j.Agent] {
				return fmt.Errorf("line %d: cron %q names agent %q, which this kit does not declare", j.Line, j.Name, j.Agent)
			}
			continue
		}
		matches := k.ToolMatches(j.Tool)
		switch len(matches) {
		case 0:
			return fmt.Errorf("line %d: cron %q names tool %q, which this kit does not declare", j.Line, j.Name, j.Tool)
		case 1:
		default:
			// Two scopes may each legally declare the same tool name (the
			// duplicate check is per-scope, not kit-wide). A cron tool job
			// names no agent to disambiguate, so first-match-wins would run
			// whichever function happened to parse first, silently.
			scopes := make([]string, len(matches))
			for i, m := range matches {
				scopes[i] = m.Scope
			}
			return fmt.Errorf("line %d: cron %q names tool %q, which is declared in more than one scope (%s) — rename one so resolution is unambiguous", j.Line, j.Name, j.Tool, strings.Join(scopes, ", "))
		}
		for pname, p := range matches[0].Tool.Params {
			if p.Required {
				return fmt.Errorf("line %d: cron %q runs tool %q, which requires argument %q — a cron tool job passes no arguments", j.Line, j.Name, j.Tool, pname)
			}
		}
	}
	return nil
}

// attach files a tool or test onto whichever scope is open.
func attach(a *Agent, g *Group, d decl, f fnDef) error {
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
