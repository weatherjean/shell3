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
	"github.com/weatherjean/shell3/internal/modelproxy"
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

// Parts is the session-independent runtime assembly: everything one process
// shares across N sessions. Front-ends derive per-session chat.Configs from it
// via SessionConfig.
//
// Concurrency: all exported methods are safe for concurrent use by multiple
// sessions. The Lua VM (luacfg.LoadedConfig) serialises access with a mutex.
// The history store is database/sql over SQLite — safe for concurrent callers.
// The proxy spawner and MCP manager are each mutex-guarded internally.
// AgentRuntime builds a fresh LLM client per call, so no client state is
// shared across sessions.
//
// Lifetime: Parts must not be used after the cleanup function returned by
// BuildParts has run. The cleanup closes the store, Lua state, MCP servers,
// and log; any method call after cleanup has undefined behaviour.
type Parts struct {
	lc     *luacfg.LoadedConfig
	st     *store.Store
	mcpMgr *mcp.Manager
	proxy  *modelproxy.Spawner
	log    applog.Logger
	uuid   string
	root   string // runtime root workdir (Options.CWD)
}

// Store returns the SQLite history store (nil when history is not enabled by any agent).
func (p *Parts) Store() *store.Store { return p.st }

// Log returns the rotating application logger.
func (p *Parts) Log() applog.Logger { return p.log }

// ProjectRef returns the project UUID used to namespace store entries and paths.
func (p *Parts) ProjectRef() string { return p.uuid }

// Root returns the runtime root working directory (the CWD passed to BuildParts).
func (p *Parts) Root() string { return p.root }

// AgentNames returns declared agent names in declaration order.
func (p *Parts) AgentNames() []string {
	agents := p.lc.Agents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// CustomTool exposes the Lua custom-tool dispatcher.
func (p *Parts) CustomTool(ctx context.Context, name, args string) (string, error) {
	return p.lc.CallTool(ctx, name, args)
}

// MCPTool dispatches a prefixed MCP tool call; errors when no servers exist.
func (p *Parts) MCPTool(ctx context.Context, name, args string) (string, error) {
	if p.mcpMgr == nil {
		return "", fmt.Errorf("no MCP servers configured")
	}
	return p.mcpMgr.Dispatch(ctx, name, args)
}

// AgentRuntime assembles the full chat runtime for the named agent: its model
// client, persona, tool defs, and guard closure. name "" uses the first
// declared agent. An unknown non-empty name returns an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	var a luacfg.Agent
	if name == "" {
		a = p.lc.FirstAgent()
	} else {
		var ok bool
		a, ok = p.lc.AgentByName(name)
		if !ok {
			return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
		}
	}
	return p.runtimeForAgent(a)
}

// subagentToAgent adapts a registered subagent to the luacfg.Agent shape that
// runtimeForAgent/BuildPersonaFor/OnToolCallFor consume. Subagents is left empty
// on purpose (depth limit 1). Keep in sync with luacfg.Subagent's fields.
func subagentToAgent(sa luacfg.Subagent) luacfg.Agent {
	return luacfg.Agent{
		Name:           sa.Name,
		ModelName:      sa.ModelName,
		Prompt:         sa.Prompt,
		Gates:          sa.Gates,
		CustomTools:    sa.CustomTools,
		MCPServerNames: sa.MCPServerNames,
		Skills:         sa.Skills,
		SkillsDisabled: sa.SkillsDisabled,
		Guard:          sa.Guard,
	}
}

// SubagentRuntime builds the runtime for a registered subagent (spawned via
// spawn_agent). Subagents never appear in AgentNames/the Tab rotation and get
// no spawn tooling of their own (depth limit 1).
func (p *Parts) SubagentRuntime(name string) (chat.ActiveAgent, error) {
	sa, ok := p.lc.SubagentByName(name)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("unknown subagent %q", name)
	}
	return p.runtimeForAgent(subagentToAgent(sa))
}

// runtimeForAgent assembles the full chat runtime for the given agent value.
// It is the common implementation shared by AgentRuntime and SubagentRuntime.
func (p *Parts) runtimeForAgent(a luacfg.Agent) (chat.ActiveAgent, error) {
	md, ok := p.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	customDefs := p.lc.CustomToolsFor(a.CustomTools)
	hasSkills := a.SkillsActive()
	toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	// Merge this agent's selected MCP servers' tools (prefixed server__tool).
	var mcpNames map[string]bool
	if p.mcpMgr != nil && len(a.MCPServerNames) > 0 {
		mcpDefs, err := p.mcpMgr.ToolDefinitionsFor(context.Background(), a.MCPServerNames)
		if err != nil {
			p.log.Warn("mcp: tool discovery failed; server tools unavailable", "error", err)
		} else {
			toolDefs = append(toolDefs, mcpDefs...)
			for _, d := range mcpDefs {
				toolNames = append(toolNames, d.Name)
			}
			mcpNames = p.mcpMgr.ToolNamesFor(a.MCPServerNames)
		}
	}

	// Inject spawn_agent + list_agents when this agent has registered subagents.
	if len(a.Subagents) > 0 {
		infos := make([]luacfg.SubagentInfo, 0, len(a.Subagents))
		for _, saName := range a.Subagents {
			sa, ok := p.lc.SubagentByName(saName)
			if !ok {
				continue // load-time validation already guarantees resolution; defensive
			}
			infos = append(infos, luacfg.SubagentInfo{Name: sa.Name, Description: sa.Description})
		}
		spawnDefs := luacfg.SpawnToolDefs(infos)
		toolDefs = append(toolDefs, spawnDefs...)
		for _, d := range spawnDefs {
			toolNames = append(toolNames, d.Name)
		}
	}

	prompt := p.lc.BuildPersonaFor(a)

	customNames := make(map[string]bool, len(a.CustomTools))
	for _, n := range a.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	agent := a // capture for the guard closure
	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: prompt,
			Tools:        toolDefs,
			Parameters:   rp,
		},
		ToolGuard: func(ctx context.Context, t string, prm map[string]any) (int, string, error) {
			d, r, e := p.lc.OnToolCallFor(agent, ctx, t, prm)
			return int(d), r, e
		},
		ModeLabel:       a.Name,
		ActiveSkills:    a.Skills,
		ActiveTools:     toolNames,
		Subagents:       a.Subagents,
		CustomToolNames: customNames,
		MCPToolNames:    mcpNames,
		LLM:             client,
		Params:          rp,
		ModelID:         md.ModelID,
		ContextWindow:   md.ContextWindow,
	}, nil
}

// RefreshPromptFor re-renders the named agent's or subagent's system prompt
// (used by /clear). name may be a declared agent name or a registered subagent
// name; callers are expected to pass names that were previously validated by a
// successful AgentRuntime/SubagentRuntime call (names come from ModeLabel,
// which is set to a.Name only on a successful lookup). The FirstAgent fallback
// exists only so an impossible miss degrades to a sane prompt rather than
// panicking; in correct use that branch is never reached.
func (p *Parts) RefreshPromptFor(name string) string {
	if a, ok := p.lc.AgentByName(name); ok {
		return p.lc.BuildPersonaFor(a)
	}
	if sa, ok := p.lc.SubagentByName(name); ok {
		return p.lc.BuildPersonaFor(subagentToAgent(sa))
	}
	return p.lc.BuildPersonaFor(p.lc.FirstAgent())
}

// SessionOptions parameterizes one session derived from shared Parts.
type SessionOptions struct {
	Agent    string // "" → first declared
	WorkDir  string // "" → runtime root
	Headless bool
	OutPath  string
	// DisableSubagents force-strips spawn_agent/list_agents from this session's
	// schema regardless of the agent's tools.subagents gate. The runtime sets it
	// for spawned subagents to enforce depth-limit 1.
	DisableSubagents bool
	// Subagent, when non-empty, runs the named subagent's config instead of an
	// agent (resolved from the subagent registry). Set by spawn_agent. Mutually
	// exclusive with Agent in practice.
	Subagent string
}

// stripSubagentTools removes spawn_agent/list_agents from an agent's schema,
// enforcing the depth-limit-1 rule for spawned subagents. AgentRuntime returns
// chat.ActiveAgent by value with freshly built slices, but [:0:0] makes a fresh
// backing array so the strip can never alias a shared/cached persona slice.
func stripSubagentTools(a chat.ActiveAgent) chat.ActiveAgent {
	keep := a.Personality.Tools[:0:0]
	for _, t := range a.Personality.Tools {
		if t.Name == "spawn_agent" || t.Name == "list_agents" {
			continue
		}
		keep = append(keep, t)
	}
	a.Personality.Tools = keep
	names := a.ActiveTools[:0:0]
	for _, n := range a.ActiveTools {
		if n == "spawn_agent" || n == "list_agents" {
			continue
		}
		names = append(names, n)
	}
	a.ActiveTools = names
	return a
}

// SessionConfig derives a per-session chat.Config from the shared parts.
// The returned config embeds per-session closures (RefreshPrompt, SwitchAgent)
// that consult only declared config plus the session's own agent choice.
func (p *Parts) SessionConfig(so SessionOptions) (chat.Config, error) {
	workdir := so.WorkDir
	if workdir == "" {
		workdir = p.root
	}
	var rt chat.ActiveAgent
	var err error
	if so.Subagent != "" {
		rt, err = p.SubagentRuntime(so.Subagent)
	} else {
		rt, err = p.AgentRuntime(so.Agent)
	}
	if err != nil {
		return chat.Config{}, err
	}
	if so.DisableSubagents {
		rt = stripSubagentTools(rt)
	}
	// activeName is the session's agent pointer, shared by the two closures
	// below; pkg/shell3.Session.SwitchAgent is documented single-threaded
	// (between turns), so a plain pointer is sufficient.
	activeName := rt.ModeLabel
	cfg := chat.Config{
		Store:         p.st,
		WorkDir:       workdir,
		ProjectRef:    p.uuid,
		CustomTool:    p.CustomTool,
		MCPTool:       p.MCPTool,
		Log:           p.log,
		OutPath:       so.OutPath,
		Headless:      so.Headless,
		AgentNames:    p.AgentNames(),
		RefreshPrompt: func() string { return p.RefreshPromptFor(activeName) },
	}
	cfg.SwitchAgent = func(name string) (chat.ActiveAgent, error) {
		// "" means "use the first agent" during initial session selection only
		// (AgentRuntime's contract). Switching to "" is a caller bug.
		if name == "" {
			return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
		}
		nrt, err := p.AgentRuntime(name)
		if err != nil {
			return chat.ActiveAgent{}, err
		}
		activeName = nrt.ModeLabel
		return nrt, nil
	}
	cfg.ApplyActiveAgent(rt)
	return cfg, nil
}

// Build assembles a single-session chat.Config — the historical entry point,
// now a wrapper over BuildParts + SessionConfig. Multi-session hosts use
// BuildParts directly via pkg/shell3.Runtime.
func Build(opts Options) (chat.Config, func(), error) {
	parts, cleanup, err := BuildParts(opts)
	if err != nil {
		return chat.Config{}, cleanup, err
	}
	cfg, err := parts.SessionConfig(SessionOptions{
		Agent: opts.Agent, WorkDir: opts.CWD, Headless: opts.Headless, OutPath: opts.OutPath,
	})
	if err != nil {
		cleanup()
		return chat.Config{}, func() {}, err
	}
	return cfg, cleanup, nil
}

// BuildParts assembles the shared runtime parts. The returned cleanup closes
// the store, Lua state, MCP servers, and log; callers MUST invoke it once.
func BuildParts(opts Options) (*Parts, func(), error) {
	b := &builder{opts: opts}
	noop := func() {}
	if err := b.resolvePaths(); err != nil {
		return nil, noop, err
	}
	b.openLog()
	b.proxy = modelproxy.New(b.l.Root, b.log)
	if err := b.loadConfig(); err != nil {
		b.closeAll()
		return nil, noop, err
	}
	b.openStore()
	b.buildMCP()
	p := &Parts{lc: b.lc, st: b.st, mcpMgr: b.mcpMgr, proxy: b.proxy,
		log: b.log, uuid: b.uuid, root: b.opts.CWD}
	return p, b.closeAll, nil
}

// builder accumulates the state and open resources used to assemble the shared
// Parts across BuildParts' stages. closers is a LIFO teardown stack: stages
// push a closer as they acquire a resource, and closeAll runs them in
// reverse-acquisition order — matching the original cleanup ordering
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
	proxy  *modelproxy.Spawner

	closers []func() // LIFO teardown stack
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
	return "", fmt.Errorf("no shell3.lua found — run 'shell3 boot' to create one (or pass --config)")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
