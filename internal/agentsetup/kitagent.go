package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
// Wiring (models, telegram) still comes from shell3.yaml; a kit's `shell3:`
// block is parsed but not yet consumed here.
func (p *Parts) LoadKit(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read kit: %w", err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		return err
	}
	p.kit, p.kitPath = k, path
	return nil
}

// Kit returns the loaded kit, or nil when none was loaded.
func (p *Parts) Kit() *kit.Kit { return p.kit }

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
		modelName = p.lc.FirstAgent().ModelName
	}
	md, ok := p.lc.Model(modelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("kit agent %q references unknown model %q", r.Agent.Name, modelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	gates := config.ToolGates{}
	for _, b := range r.Builtins {
		switch b {
		case "bash":
			gates.Bash = true
		case "bash_bg":
			gates.BashBg = true
		case "edit":
			gates.Edit = true
		case "media":
			gates.Media = true
		case "read":
			gates.Read = true
		case "list_files":
			gates.List = true
		case "history":
			gates.History = true
		}
	}

	defs := append(config.ToolDefs(gates), r.ToolDefs()...)
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}

	// context: files are re-read at every turn start, so an agent's memory.md
	// is current on every dispatch rather than a session-creation snapshot.
	ctxFiles := r.Agent.Context

	// Declared tools must route to the host-tool dispatcher, not the built-in
	// tool handler — without this the model calls them and the turn answers
	// with an unknown-tool error.
	hostNames := p.KitHostToolNames(r)

	skillNames := make([]string, 0, len(r.Skills))
	for _, s := range r.Skills {
		skillNames = append(skillNames, s.Name)
	}

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         r.Agent.Name,
			SystemPrompt: r.SystemPrompt() + config.RenderContext(p.configDir, ctxFiles),
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
			Environment:   true,
			ContextWindow: md.ContextWindow,
			CompactAt:     md.CompactAt,
			KeepRecent:    md.KeepRecent,
			PruneAt:       md.PruneAt,
		},
	}, nil
}

// KitHostToolNames is the set of declared tool names that must route to the
// host-tool dispatcher for this agent.
func (p *Parts) KitHostToolNames(r kit.Resolved) map[string]bool {
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
