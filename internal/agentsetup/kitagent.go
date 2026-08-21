package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/persona"
)

// errNoSuchKitAgent marks "this kit does not declare that agent", which is a
// fall-through signal (try the markdown config) rather than a failure.
var errNoSuchKitAgent = errors.New("agentsetup: no such kit agent")

// LoadKit reads and parses a kit file, attaching it to these Parts. Agents
// declared in the kit then resolve through KitAgentRuntime, and their declared
// tools dispatch through KitHostTool.
//
// Wiring (models, telegram, dash_port, …) comes from the kit's `shell3:`
// block, re-marshalled through the YAML parser by config.readWiring.
func (p *Parts) LoadKit(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read kit: %w", err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		return err
	}
	// Cross-reference: an agent opting into an MCP server the wiring does not
	// declare is a typo that would otherwise present as silently missing
	// tools.
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
	// The kit's `gate:` / `note:` blocks govern tool calls and tool results,
	// standing in for the hooks/*.sh files a markdown config uses.
	if len(k.Gates) > 0 || len(k.Notes) > 0 {
		main := ""
		if len(k.Agents) > 0 {
			main = k.Agents[0].Name
		}
		p.lc.SetKitHooks(path, main, k.Gates, k.Notes)
	}
	return nil
}

// Kit returns the loaded kit, or nil when none was loaded.
func (p *Parts) Kit() *kit.Kit { return p.kit }

// KitPath returns the path the loaded kit was read from ("" when no kit is
// loaded) — a cron tool job's ToolRunner needs it to source the kit before
// running a tool's shell function.
func (p *Parts) KitPath() string { return p.kitPath }

// KitToolByName finds a declared tool anywhere in the loaded kit, regardless
// of which agent's scope declares it. A cron tool job names no agent — there
// is no per-agent Resolved capability set to search — so this searches the
// whole kit, the operator's own declaration being the trust boundary. false
// when no kit is loaded.
func (p *Parts) KitToolByName(name string) (kit.Tool, bool) {
	if p.kit == nil {
		return kit.Tool{}, false
	}
	return p.kit.ToolByName(name)
}

// KitAgent resolves one agent declared in the loaded kit into its full
// capability set. The first declared agent is the main agent — it gets every
// built-in; everyone else gets exactly what they declare.
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

// KitAgentRuntime assembles the chat runtime for a kit-declared agent: its
// model client, a persona whose prompt carries the agent's skills inline, and a
// tool list that is the built-ins it asked for plus every declared tool it can
// call.
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

	// Delegation: the main agent gets the task tool with every other kit agent
	// as an allowed target. Employees never get it — delegation is one level,
	// structurally, not by convention.
	var employees []string
	if isMain(p.kit, r.Agent.Name) {
		refs := make([]config.SubagentRef, 0, len(p.kit.Agents))
		for i, ka := range p.kit.Agents {
			if i == 0 {
				continue
			}
			employees = append(employees, ka.Name)
			refs = append(refs, config.SubagentRef{Name: ka.Name, Description: ka.Desc})
		}
		if len(refs) > 0 {
			defs = append(defs, config.TaskToolFor(refs), config.TaskListTool, config.TaskStatusTool, config.TaskCancelTool)
		}
	}

	// Declared tools must route to the host-tool dispatcher, not the built-in
	// tool handler. The `mcp:` opt-in adds its servers' tools to the same
	// dispatch path.
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

	// Skills are FILES, indexed by name + description + path — never inlined.
	// Inlining every skill body cost thousands of tokens on every turn and
	// removed the agent's ability to choose which one to read. The prompt
	// itself (skills index + `context:` bodies, re-read at every turn start)
	// comes from kitPrompt, shared with RefreshPromptFor.
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

// isMain reports whether name is the kit's first-declared agent.
func isMain(k *kit.Kit, name string) bool {
	return len(k.Agents) > 0 && k.Agents[0].Name == name
}

// kitHostToolNames is the set of declared tool names that must route to the
// host-tool dispatcher for this agent.
func (p *Parts) kitHostToolNames(r kit.Resolved) map[string]bool {
	out := map[string]bool{}
	for _, t := range r.Tools {
		out[t.Name] = true
	}
	return out
}

// KitHostTool builds the dispatcher for an agent's declared tools. A call
// arrives as the model's JSON arguments, is validated against the manifest, and
// runs as the tool's shell function with each param exported.
//
// A name this agent cannot call returns chat.ErrHostToolNotFound so the caller
// falls through to its normal unknown-tool handling rather than failing the turn.
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

// defaultModelName is the model an agent that declares none runs on: the kit's
// main agent's model, falling back to the first model the wiring declares (a
// one-model config need name it nowhere).
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

// kitContextBase is where an agent's `context:` paths resolve: its OWN
// workdir when it declares one, the config dir otherwise. Without this every
// employee's `context: [memory.md]` would silently load the main agent's
// memory instead of its own.
func (p *Parts) kitContextBase(a kit.Agent) string {
	if a.Workdir != "" {
		return expandHomePath(a.Workdir, p.home)
	}
	return p.configDir
}

// kitPrompt renders a kit agent's system prompt: its authored body, the
// skills index, and the `context:` files read fresh. The single source shared
// by KitAgentRuntime and RefreshPromptFor, so a refreshed prompt can never
// drift from the one the session was built with.
func (p *Parts) kitPrompt(r kit.Resolved) string {
	skills := config.ScanSkills(p.kitAgentSkillDir(r.Agent.Name))
	return r.Agent.Prompt + config.RenderSkills(skills) +
		config.RenderContext(p.kitContextBase(r.Agent), r.Agent.Context)
}
