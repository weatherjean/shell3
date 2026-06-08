// Package agentsetup is the shared config assembly used by every shell3
// front-end (the bubbletea TUI, the stdout one-shot, and the pkg/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads shell3.lua, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/mcp"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
)

// Options parameterizes Build. CWD/HomeDir default via the caller (front-ends
// pass os.Getwd()/os.UserHomeDir()). ConfigPath "" triggers default resolution.
type Options struct {
	ConfigPath string
	CWD        string
	HomeDir    string
	Headless   bool
	OutPath    string
	// Agent selects the initial active agent by name. Empty uses the first
	// declared agent. A non-empty name with no match makes Build fail.
	Agent string
}

// builder accumulates the state and open resources used to assemble a
// chat.Config across Build's stages. closers is a LIFO teardown stack: stages
// push a closer as they acquire a resource, and closeAll runs them in
// reverse-acquisition order — matching Build's original cleanup ordering
// (store → lc → log).
type builder struct {
	opts Options

	configPath string
	g          paths.Global
	l          paths.Local
	proj       paths.Project
	uuid       string

	log    applog.Logger
	lc     *luacfg.LoadedConfig
	st     *store.Store
	mcpMgr *mcp.Manager

	closers []func() // LIFO teardown stack
}

// Build assembles the full chat.Config. The returned cleanup closes the store,
// the Lua state, and the log; callers MUST invoke it.
func Build(opts Options) (chat.Config, func(), error) {
	b := &builder{opts: opts}
	noop := func() {}
	if err := b.resolvePaths(); err != nil {
		return chat.Config{}, noop, err // nothing acquired yet
	}
	b.openLog() // non-fatal; may push the log closer
	if err := b.loadConfig(); err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	// Select the initial active agent before assembling its runtime, so the
	// persona/tools/model are built for the chosen agent from the start.
	if opts.Agent != "" {
		if _, err := b.lc.SwitchAgent(opts.Agent); err != nil {
			b.closeAll()
			return chat.Config{}, noop, err
		}
	}
	b.openStore() // non-fatal; may push the store closer
	b.buildMCP()  // non-fatal; may push the MCP shutdown closer
	cfg, err := b.assemble()
	if err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	return cfg, b.closeAll, nil
}

// closeAll runs the teardown stack in reverse-acquisition order.
func (b *builder) closeAll() {
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}

// resolvePaths resolves the config path, builds the global/local/project path
// sets, and ensures the global and project directories exist.
func (b *builder) resolvePaths() error {
	configPath, err := ResolveConfigPath(b.opts.ConfigPath, b.opts.CWD, b.opts.HomeDir)
	if err != nil {
		return err
	}
	b.configPath = configPath
	b.g = paths.NewGlobal(b.opts.HomeDir)
	b.l = paths.NewLocal(b.opts.CWD)
	if err := bootstrap.EnsureGlobal(b.g); err != nil {
		return err
	}
	uuid, err := bootstrap.EnsureProject(b.l, b.g, b.opts.CWD)
	if err != nil {
		return err
	}
	b.uuid = uuid
	b.proj = paths.NewProject(b.g, uuid)
	return nil
}

// openLog opens the rotating app log. Failure is non-fatal: it warns on stderr
// (the log itself being unavailable to record it) and falls back to Noop.
func (b *builder) openLog() {
	const logMaxBytes = 2 * 1024 * 1024
	const logArchives = 3
	log, logCloser, err := applog.Open(b.g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		b.log = applog.Noop{}
		return
	}
	b.log = log
	b.closers = append(b.closers, func() { _ = logCloser.Close() })
}

// loadConfig loads shell3.lua. The Lua/.env workdir is the config file's
// directory; the agent's bash cwd stays opts.CWD. These differ on purpose.
func (b *builder) loadConfig() error {
	lc, err := luacfg.Load(b.configPath, filepath.Dir(b.configPath))
	if err != nil {
		return err
	}
	b.lc = lc
	b.closers = append(b.closers, func() { lc.Close() })
	return nil
}

// openStore opens the SQLite store when any agent gates history. Non-fatal: a
// failure warns and proceeds.
func (b *builder) openStore() {
	needsStore := false
	for _, a := range b.lc.Agents() {
		if a.Gates.History {
			needsStore = true
			break
		}
	}
	if needsStore {
		if s, e := store.Open(b.proj.DB); e == nil {
			b.st = s
			b.closers = append(b.closers, func() { _ = s.Close() })
		} else {
			b.log.Warn("open store failed — history unavailable", "error", e)
		}
	}
}

// buildMCP constructs the MCP manager from all declared servers. The schema
// cache lives under the project dir so discovered tools persist across runs.
// No-op when no servers are declared.
func (b *builder) buildMCP() {
	servers := b.lc.MCPServers
	if len(servers) == 0 {
		return
	}
	specs := make([]mcp.Spec, 0, len(servers))
	for _, s := range servers {
		specs = append(specs, mcp.Spec{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			Tools:   s.Tools,
		})
	}
	cacheDir := filepath.Join(b.proj.Dir, "mcp")
	b.mcpMgr = mcp.NewManager(specs, cacheDir)
	b.closers = append(b.closers, func() { b.mcpMgr.Shutdown() })
}

// buildActiveRuntime assembles the full chat runtime for the currently active
// agent: its model client, persona, tool defs, and guard closure. Called at
// startup and on every agent switch.
func (b *builder) buildActiveRuntime() (chat.ActiveAgent, error) {
	a := b.lc.Active()
	md, ok := b.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	client, rp := buildClient(md)

	customDefs := b.lc.CustomToolsFor(a.CustomTools)
	hasSkills := a.SkillsActive()
	toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	// Merge this agent's selected MCP servers' tools (prefixed server__tool).
	var mcpNames map[string]bool
	if b.mcpMgr != nil && len(a.MCPServerNames) > 0 {
		mcpDefs, err := b.mcpMgr.ToolDefinitionsFor(context.Background(), a.MCPServerNames)
		if err != nil {
			b.log.Warn("mcp: tool discovery failed; server tools unavailable", "error", err)
		} else {
			toolDefs = append(toolDefs, mcpDefs...)
			for _, d := range mcpDefs {
				toolNames = append(toolNames, d.Name)
			}
			mcpNames = b.mcpMgr.ToolNamesFor(a.MCPServerNames)
		}
	}

	prompt := b.lc.BuildPersona()

	customNames := make(map[string]bool, len(a.CustomTools))
	for _, n := range a.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: prompt,
			Tools:        toolDefs,
			Parameters:   rp,
		},
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := b.lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		ModeLabel:       a.Name,
		ActiveSkills:    a.Skills,
		ActiveTools:     toolNames,
		CustomToolNames: customNames,
		MCPToolNames:    mcpNames,
		LLM:             client,
		Params:          rp,
		ModelID:         md.ModelID,
		ContextWindow:   md.ContextWindow,
	}, nil
}

// assemble renders the active agent's runtime and builds the final chat.Config,
// including the buildPrompt / switchAgent closures stored into it.
func (b *builder) assemble() (chat.Config, error) {
	// buildPrompt re-renders the active agent's system prompt. Used by /clear
	// (cfg.RefreshPrompt) so a new conversation re-renders against whatever
	// agent is active at that moment.
	buildPrompt := func() string {
		return b.lc.BuildPersona()
	}
	switchAgent := func(name string) (chat.ActiveAgent, error) {
		if _, err := b.lc.SwitchAgent(name); err != nil {
			return chat.ActiveAgent{}, err
		}
		return b.buildActiveRuntime()
	}

	rt, err := b.buildActiveRuntime()
	if err != nil {
		return chat.Config{}, err
	}

	agents := b.lc.Agents()
	agentNames := make([]string, 0, len(agents))
	for _, a := range agents {
		agentNames = append(agentNames, a.Name)
	}

	// Agent-independent fields are set here once; the agent-derived fields
	// (LLM, persona, params, guard, tool/skill sets, context window, status
	// line) are filled by ApplyActiveAgent — the same method every front-end
	// uses on a switch, so initial assembly and switching can never drift.
	cfg := chat.Config{
		Store:         b.st,
		RefreshPrompt: buildPrompt,
		WorkDir:       b.opts.CWD,
		ProjectRef:    b.uuid,
		CustomTool:    b.lc.CallTool,
		MCPTool: func(ctx context.Context, name, args string) (string, error) {
			if b.mcpMgr == nil {
				return "", fmt.Errorf("no MCP servers configured")
			}
			return b.mcpMgr.Dispatch(ctx, name, args)
		},
		Log:         b.log,
		OutPath:     b.opts.OutPath,
		Headless:    b.opts.Headless,
		AgentNames:  agentNames,
		SwitchAgent: switchAgent,
	}
	cfg.ApplyActiveAgent(rt)
	return cfg, nil
}

// buildClient constructs a streaming client plus its request params from a
// configured model. Reused for the initial client and on each agent switch.
func buildClient(md luacfg.Model) (chat.LLMClient, llm.RequestParams) {
	cl := openai.NewClient(md.BaseURL, md.APIKey, md.ModelID)
	rp := llm.RequestParams{
		ReasoningEffort: md.Reasoning,
		MaxTokens:       md.MaxTokens,
		Temperature:     md.Temperature,
	}
	cl.SetParams(rp)
	if md.Extra != nil {
		cl.SetExtra(md.Extra)
	}
	return cl, rp
}

// ResolveConfigPath returns the shell3.lua to load: the explicit flag, else
// ./shell3.lua if it exists, else ~/.shell3/shell3.lua if it exists. Returns
// an error when nothing is found.
func ResolveConfigPath(flag, cwd, homeDir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	local := filepath.Join(cwd, "shell3.lua")
	if fileExists(local) {
		return local, nil
	}
	global := filepath.Join(homeDir, ".shell3", "shell3.lua")
	if fileExists(global) {
		return global, nil
	}
	return "", fmt.Errorf("no shell3.lua found — pass --config or create ~/.shell3/shell3.lua")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
