package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/persona"
)

// errNoSuchKitAgent marks "this kit does not declare that agent".
var errNoSuchKitAgent = errors.New("agentsetup: no such kit agent")

// LoadKit attaches the kit config.Load already parsed: its agents resolve
// through KitAgentRuntime and their tools dispatch through KitHostTool. path
// is where it was read from — a cron tool job sources it. The wiring comes
// from the same parse, lifted by config.readWiring.
func (p *Parts) LoadKit(path string) error {
	k := p.lc.Kit()
	if k == nil {
		return fmt.Errorf("no kit loaded from %s", path)
	}
	// An agent opting into an undeclared MCP server is a typo that would
	// otherwise present as silently missing tools.
	for _, a := range k.Agents {
		if a.MCPAll {
			continue
		}
		for _, name := range a.MCP {
			if !p.lc.HasMCPServer(name) {
				return fmt.Errorf("%s: agent %q opts into unknown mcp server %q", filepath.Base(path), a.Name, name)
			}
		}
	}
	p.kit, p.kitPath = k, path
	// gate:/note: govern tool calls and results, event: observes the stream,
	// command: answers a host command with no model turn.
	if h := KitHooksOf(k); !h.Empty() {
		main := ""
		if len(k.Agents) > 0 {
			main = k.Agents[0].Name
		}
		p.lc.SetKitHooks(path, main, h)
	}
	return nil
}

// KitHooksOf projects a kit's hook declarations into the shape config
// installs, here rather than in internal/kit so the kit package keeps knowing
// nothing about how hooks are run.
func KitHooksOf(k *kit.Kit) config.KitHooks {
	h := config.KitHooks{Gates: k.Gates, Notes: k.Notes}
	if len(k.Events) > 0 {
		h.Events = map[string]config.EventSub{}
		for agent, e := range k.Events {
			h.Events[agent] = config.EventSub{Func: e.Func, On: e.On}
		}
	}
	if len(k.Commands) > 0 {
		h.Commands = map[string]string{}
		for name, c := range k.Commands {
			h.Commands[name] = c.Func
		}
	}
	return h
}

// Kit returns the loaded kit, or nil when none was loaded.
func (p *Parts) Kit() *kit.Kit { return p.kit }

// KitPath is where the loaded kit was read from, "" when none is; kit.Runner
// sources it before running a tool's function.
func (p *Parts) KitPath() string { return p.kitPath }

// KitAgent resolves one declared agent into its capability set. The first
// declared agent gets every built-in; everyone else gets what they declare.
func (p *Parts) KitAgent(name string) (kit.Resolved, error) {
	if p.kit == nil {
		return kit.Resolved{}, errNoSuchKitAgent
	}
	for i, a := range p.kit.Agents {
		if a.Name == name || (name == "" && i == 0) {
			return p.kit.Resolve(a, i == 0)
		}
	}
	return kit.Resolved{}, fmt.Errorf("%w: kit %s declares no agent %q", errNoSuchKitAgent, p.kitPath, name)
}

// KitAgentRuntime assembles a kit agent's runtime: model client, persona, and
// the built-ins it asked for plus every declared tool it can call.
func (p *Parts) KitAgentRuntime(name string) (chat.ActiveAgent, error) {
	r, err := p.KitAgent(name)
	if err != nil {
		return chat.ActiveAgent{}, err
	}

	modelName := r.Agent.Model
	if modelName == "" {
		modelName = p.defaultModelName()
	}
	md, ok := p.lc.Model(modelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("kit agent %q references unknown model %q", r.Agent.Name, modelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	defs := append(config.ToolDefs(r.Builtins), r.ToolDefs()...)

	// EVERY agent that has someone to dispatch gets the task tool, employees
	// included: delegation is two levels, and the bound is enforced at
	// DISPATCH time by the handler, never by hiding the schema. An absent
	// tool is an invitation to improvise — the failure this replaced was an
	// employee hand-rolling an HTTP client against a model API instead of
	// saying "this needs delegating". Main is never a target (it is the
	// conversation, not a worker) and neither is the caller itself.
	var employees []string
	refs := make([]config.SubagentRef, 0, len(p.kit.Agents))
	for i, ka := range p.kit.Agents {
		if i == 0 || ka.Name == r.Agent.Name {
			continue
		}
		employees = append(employees, ka.Name)
		refs = append(refs, config.SubagentRef{Name: ka.Name, Description: ka.Desc})
	}
	if len(refs) > 0 {
		defs = append(defs, config.TaskToolFor(refs), config.TaskListTool, config.TaskStatusTool, config.TaskCancelTool)
	}

	// Declared tools route to the host-tool dispatcher, not the built-in
	// handler; the mcp: opt-in joins the same path.
	hostNames := p.kitHostToolNames(r)
	if p.mcp != nil && (r.Agent.MCPAll || len(r.Agent.MCP) > 0) {
		mcpDefs := p.mcp.Tools(r.Agent.MCP, r.Agent.MCPAll)
		if len(mcpDefs) > 0 {
			defs = append(defs, mcpDefs...)
			for _, d := range mcpDefs {
				hostNames[d.Name] = true
			}
		}
	}

	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}

	// Skills are FILES, indexed by name, description and path — never
	// inlined, which cost thousands of tokens per turn and removed the
	// agent's choice of which to read. The prompt comes from kitPrompt,
	// shared with RefreshPromptFor.
	skills := config.ScanSkills(p.kitAgentSkillDir(r.Agent.Name))
	skillNames := make([]string, 0, len(skills))
	for _, sk := range skills {
		skillNames = append(skillNames, sk.Name)
	}

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         r.Agent.Name,
			SystemPrompt: p.kitPrompt(r),
			Tools:        defs,
		},
		ModeLabel:    r.Agent.Name,
		ActiveTools:  names,
		ActiveSkills: skillNames,
		LLM:          client,
		Params:       rp,
		ModelID:      md.ModelID,
		AgentKnobs: chat.AgentKnobs{
			HostToolNames: hostNames,
			Subagents:     employees,
			Environment:   true,
			ContextWindow: md.ContextWindow,
			CompactAt:     md.CompactAt,
			KeepRecent:    md.KeepRecent,
			PruneAt:       md.PruneAt,
		},
	}, nil
}

// expandHomePath resolves a leading ~/ against home.
func expandHomePath(p, home string) string {
	if strings.HasPrefix(p, "~/") && home != "" {
		return filepath.Join(home, p[2:])
	}
	return p
}

// AgentContextBase is where an agent's context: paths resolve: its OWN
// workdir when it declares one (~/ expanded, a relative one against the config
// dir), the config dir otherwise. Without it every employee's
// `context: [memory.md]` would load the main agent's memory. Exported because
// `shell3 health` must inspect the same files the agent loads — a second copy
// of this rule would let health pass on a file nothing reads.
//
// TODO: SubagentWorkdir expands ~/ but leaves a RELATIVE workdir alone, so the
// shell runs it against the process cwd while context: resolves it against the
// config dir. One of the two is wrong; decide which.
func AgentContextBase(configDir, home, workdir string) string {
	if workdir == "" {
		return configDir
	}
	if p := expandHomePath(workdir, home); p != workdir || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(configDir, workdir)
}

// isMain reports whether name is the kit's first-declared agent.
func isMain(k *kit.Kit, name string) bool {
	return len(k.Agents) > 0 && k.Agents[0].Name == name
}

// kitHostToolNames are this agent's declared tools, which route to the host
// dispatcher.
func (p *Parts) kitHostToolNames(r kit.Resolved) map[string]bool {
	out := map[string]bool{}
	for _, t := range r.Tools {
		out[t.Name] = true
	}
	return out
}

// KitHostTool dispatches an agent's declared tools: the model's JSON arguments
// are validated against the manifest, then run as the tool's shell function
// with each param exported. A name this agent cannot call returns
// chat.ErrHostToolNotFound, so the caller falls through to its own unknown-tool
// handling rather than failing the turn.
func (p *Parts) KitHostTool(r kit.Resolved, workDir string) func(context.Context, string, string) (string, error) {
	path := p.kitPath
	return func(ctx context.Context, name, argsJSON string) (string, error) {
		t, ok := r.ToolByName(name)
		if !ok {
			return "", chat.ErrHostToolNotFound
		}
		args := map[string]any{}
		if argsJSON != "" {
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", fmt.Errorf("tool %q: arguments are not valid JSON: %w", name, err)
			}
		}
		return kit.Runner{Path: path, Dir: workDir}.Run(ctx, t, args)
	}
}

// defaultModelName is what an agent declaring none runs on: the main agent's
// model, else the first the wiring declares, so a one-model config names it
// nowhere.
func (p *Parts) defaultModelName() string {
	if p.kit != nil && len(p.kit.Agents) > 0 && p.kit.Agents[0].Model != "" {
		return p.kit.Agents[0].Model
	}
	if len(p.lc.Models) > 0 {
		return p.lc.Models[0].Name
	}
	return ""
}

// kitAgentSkillDir is where a kit agent's skills live: <config>/skills/ for
// the main agent, <config>/projects/<name>/skills/ for an employee.
func (p *Parts) kitAgentSkillDir(name string) string {
	if isMain(p.kit, name) {
		return filepath.Join(p.configDir, "skills")
	}
	return filepath.Join(p.configDir, "projects", name, "skills")
}

func (p *Parts) kitContextBase(a kit.Agent) string {
	return AgentContextBase(p.configDir, p.home, a.Workdir)
}

// kitPrompt renders an agent's system prompt — authored body, skills index,
// context: files read fresh. Shared by KitAgentRuntime and RefreshPromptFor,
// so a refreshed prompt cannot drift from the one the session was built with.
func (p *Parts) kitPrompt(r kit.Resolved) string {
	skills := config.ScanSkills(p.kitAgentSkillDir(r.Agent.Name))
	return r.Agent.Prompt + config.RenderSkills(skills) +
		config.RenderContext(p.kitContextBase(r.Agent), r.Agent.Context)
}
