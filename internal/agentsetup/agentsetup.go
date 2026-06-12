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
	"strings"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/modelproxy"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
)

// Options parameterizes BuildParts: where to find the config and which
// directories the runtime resolves against. CWD/HomeDir default via the caller
// (front-ends pass os.Getwd()/os.UserHomeDir()). Per-session concerns (agent,
// headless, out path) live in SessionOptions.
type Options struct {
	ConfigPath string // "" triggers default resolution (ResolveConfigPath)
	CWD        string
	HomeDir    string
}

// Parts is the session-independent runtime assembly: everything one process
// shares across N sessions. Front-ends derive per-session chat.Configs from it
// via SessionConfig.
//
// Concurrency: all exported methods are safe for concurrent use by multiple
// sessions. The Lua VM (luacfg.LoadedConfig) serialises access with a mutex.
// The history store is database/sql over SQLite — safe for concurrent callers.
// The proxy spawner is mutex-guarded internally.
// AgentRuntime builds a fresh LLM client per call, so no client state is
// shared across sessions.
//
// Lifetime: Parts must not be used after the cleanup function returned by
// BuildParts has run. The cleanup closes the store, Lua state, proxies,
// and log; any method call after cleanup has undefined behaviour.
type Parts struct {
	lc     *luacfg.LoadedConfig
	st     *store.Store
	proxy  *modelproxy.Spawner
	log    applog.Logger
	uuid   string
	root   string // runtime root workdir (Options.CWD)
	dbPath string // absolute path to the history SQLite DB (for the Environment section)
}

// Store returns the SQLite history store (always opened; nil only when the
// store-open itself failed, which is non-fatal and logged).
func (p *Parts) Store() *store.Store { return p.st }

// Log returns the rotating application logger.
func (p *Parts) Log() applog.Logger { return p.log }

// ProjectRef returns the project UUID used to namespace store entries and paths.
func (p *Parts) ProjectRef() string { return p.uuid }

// Root returns the runtime root working directory (the CWD passed to BuildParts).
func (p *Parts) Root() string { return p.root }

// Telegram returns the parsed shell3.telegram{} config (zero value if absent).
func (p *Parts) Telegram() luacfg.TelegramConfig { return p.lc.Telegram() }

// Cron returns the parsed shell3.cron jobs (nil if absent).
func (p *Parts) Cron() []luacfg.CronJob { return p.lc.Cron() }

// ModelCount returns the number of declared models.
func (p *Parts) ModelCount() int { return len(p.lc.Models) }

// AgentNames returns declared agent names in declaration order.
func (p *Parts) AgentNames() []string {
	agents := p.lc.Agents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// SubagentDescription returns the model-facing "when to use" description for a
// registered subagent, or ("", false) if no such subagent is declared. The
// Session uses it to render the per-session delegation context (the allowed
// subagents the active agent may spawn, each as "name: description").
func (p *Parts) SubagentDescription(name string) (string, bool) {
	sa, ok := p.lc.SubagentByName(name)
	if !ok {
		return "", false
	}
	return sa.Description, true
}

// AgentRuntime assembles the full chat runtime for the named agent: its model
// client, persona, and tool defs. name "" uses the first declared agent. An
// unknown non-empty name falls back to the subagent registry (so a spawned
// `shell3 --agent <subagent>` resolves the headless subagent config); a name in
// neither registry returns an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	if name == "" {
		return p.runtimeForAgent(p.lc.FirstAgent())
	}
	if a, ok := p.lc.AgentByName(name); ok {
		return p.runtimeForAgent(a)
	}
	// A subagent name passed via --agent (the spawn command): resolve it from the
	// subagent registry into a plain headless config. The depth-1 gate is retired
	// — whether a resolved agent gets a delegation context is decided per session
	// (pkg/shell3) by whether it lists subagents, not by a spawn-time flag.
	if sa, ok := p.lc.SubagentByName(name); ok {
		return p.runtimeForAgent(subagentToAgent(sa))
	}
	return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
}

// subagentToAgent adapts a registered subagent to the luacfg.Agent shape that
// runtimeForAgent/BuildPersonaFor consume. Subagents is left empty on purpose
// (depth limit 1). Keep in sync with luacfg.Subagent's fields.
func subagentToAgent(sa luacfg.Subagent) luacfg.Agent {
	return luacfg.Agent{
		Name:           sa.Name,
		ModelName:      sa.ModelName,
		Prompt:         sa.Prompt,
		Gates:          sa.Gates,
		CustomTools:    sa.CustomTools,
		Skills:         sa.Skills,
		SkillsDisabled: sa.SkillsDisabled,
	}
}

// runtimeForAgent assembles the full chat runtime for the given agent value.
// It is the common implementation shared by the agent and subagent resolution
// paths in AgentRuntime.
func (p *Parts) runtimeForAgent(a luacfg.Agent) (chat.ActiveAgent, error) {
	md, ok := p.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	customDefs := p.lc.CustomToolsFor(a.CustomTools)
	toolDefs := luacfg.ToolDefs(a.Gates, customDefs)
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	// Subagents are no longer an in-process tool: an agent delegates by
	// backgrounding a `shell3` subprocess (bash_bg). The per-session Delegation
	// context (concrete sink/config/binary paths + the templated spawn command)
	// is injected by pkg/shell3.Session, which can see session-level paths;
	// a.Subagents (the allowlist) is surfaced via ActiveAgent.Subagents below so
	// the Session knows which subagents this agent may spawn.

	prompt := p.lc.BuildPersonaFor(a) + p.environmentSection()

	customNames := make(map[string]bool, len(a.CustomTools))
	for _, n := range a.CustomTools {
		customNames[n] = true
	}

	// Stub tools (shell3.stub_tools) are config-global: append one minimal,
	// no-param def per stub to EVERY agent's schema so a hallucinated tool call
	// returns a redirect instead of erroring. Precedence: if a stub name collides
	// with a real/custom/spawn tool already present, SKIP the stub — the real tool
	// always wins. Surviving stubs are NOT added to customNames: the chat layer
	// routes them via cfg.StubTools (a separate, lower-precedence branch in
	// turn.go), so a stub never shadows a real tool at dispatch time.
	if stubs := p.lc.StubNames(); len(stubs) > 0 {
		present := make(map[string]bool, len(toolNames))
		for _, n := range toolNames {
			present[n] = true
		}
		for name := range stubs {
			if present[name] {
				continue // real tool wins; silently skip the colliding stub
			}
			toolDefs = append(toolDefs, llm.ToolDefinition{
				Name:        name,
				Description: "stub: not a real tool — redirects you to bash/edit_file",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			})
			toolNames = append(toolNames, name)
		}
	}

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: prompt,
			Tools:        toolDefs,
			Parameters:   rp,
		},
		ModeLabel:       a.Name,
		ActiveSkills:    a.Skills,
		ActiveTools:     toolNames,
		Subagents:       a.Subagents,
		CustomToolNames: customNames,
		LLM:             client,
		Params:          rp,
		ModelID:         md.ModelID,
		ContextWindow:   md.ContextWindow,
		CompactAt:       md.CompactAt,
	}, nil
}

// environmentSection renders the host-injected "## Environment" block appended
// to every agent's system prompt. It carries runtime values the agent can only
// learn at session start — currently the read-only history DB path, which the
// `history` skill reads via `sqlite3 'file:<path>?mode=ro'`. Kept minimal and
// factual; later phases append more lines (sink path, subagent command, …), so
// new facts are added as additional "- key: value" rows under this heading.
//
// Returns "" when no DB path is resolvable (store-open failed / nil store path),
// so the section never advertises a query target the agent cannot use.
func (p *Parts) environmentSection() string {
	if p.dbPath == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Environment\n")
	b.WriteString("Runtime paths for this session (read-only unless stated):\n")
	fmt.Fprintf(&b, "- history_db: %s (open with `sqlite3 'file:%s?mode=ro'`; see the `history` skill)\n",
		p.dbPath, p.dbPath)
	return b.String()
}

// RefreshPromptFor re-renders the named agent's or subagent's system prompt
// (used by /clear). name may be a declared agent name or a registered subagent
// name; callers are expected to pass names that were previously validated by a
// successful AgentRuntime call (names come from ModeLabel,
// which is set to a.Name only on a successful lookup). The FirstAgent fallback
// exists only so an impossible miss degrades to a sane prompt rather than
// panicking; in correct use that branch is never reached.
func (p *Parts) RefreshPromptFor(name string) string {
	env := p.environmentSection()
	if a, ok := p.lc.AgentByName(name); ok {
		return p.lc.BuildPersonaFor(a) + env
	}
	if sa, ok := p.lc.SubagentByName(name); ok {
		return p.lc.BuildPersonaFor(subagentToAgent(sa)) + env
	}
	return p.lc.BuildPersonaFor(p.lc.FirstAgent()) + env
}

// SessionOptions parameterizes one session derived from shared Parts.
type SessionOptions struct {
	Agent    string // "" → first declared (falls back to a subagent name)
	WorkDir  string // "" → runtime root
	Headless bool
	OutPath  string
}

// SessionConfig derives a per-session chat.Config from the shared parts.
// The returned config embeds per-session closures (RefreshPrompt, SwitchAgent)
// that consult only declared config plus the session's own agent choice.
func (p *Parts) SessionConfig(so SessionOptions) (chat.Config, error) {
	workdir := so.WorkDir
	if workdir == "" {
		workdir = p.root
	}
	rt, err := p.AgentRuntime(so.Agent)
	if err != nil {
		return chat.Config{}, err
	}
	// activeName is the session's agent pointer, shared by the two closures
	// below; pkg/shell3.Session.SwitchAgent is documented single-threaded
	// (between turns), so a plain pointer is sufficient.
	activeName := rt.ModeLabel
	cfg := chat.Config{
		Store:             p.st,
		WorkDir:           workdir,
		ProjectRef:        p.uuid,
		ResolveCustomTool: p.lc.ResolveCustomCall,
		StubTools:         p.lc.StubNames(),
		Log:               p.log,
		OutPath:           so.OutPath,
		Headless:          so.Headless,
		AgentNames:        p.AgentNames(),
		RefreshPrompt:     func() string { return p.RefreshPromptFor(activeName) },
	}
	// shell3.wrap_bash: a single config-global hook the bash/bash_bg tools pass
	// their command through. Wired only when declared — a nil closure means no
	// wrapping (the unsafe default: bash runs with no restrictions). Not
	// per-agent and not swapped on agent switch.
	if p.lc.HasWrapBash() {
		cfg.WrapBash = func(ctx context.Context, cmd string) ([]string, bool, string, error) {
			return p.lc.WrapBash(ctx, cmd)
		}
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

// BuildParts assembles the shared runtime parts. The returned cleanup closes
// the store, Lua state, proxies, and log; callers MUST invoke it once.
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
	p := &Parts{lc: b.lc, st: b.st, proxy: b.proxy,
		log: b.log, uuid: b.uuid, root: b.opts.CWD, dbPath: b.proj.DB}
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

	log   applog.Logger
	lc    *luacfg.LoadedConfig
	st    *store.Store
	proxy *modelproxy.Spawner

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

// openStore opens the SQLite store unconditionally: the store always persists
// the conversation (saveHistory), and the agent reads it back out-of-process
// via the `history` skill (`sqlite3 'file:<db>?mode=ro'`). There is no longer a
// gate — history is always on. Non-fatal: a failure warns and proceeds with a
// nil store (persistence and the skill's queries silently degrade).
func (b *builder) openStore() {
	if s, e := store.Open(b.proj.DB); e == nil {
		b.st = s
		b.closers = append(b.closers, func() { _ = s.Close() })
	} else {
		b.log.Warn("open store failed — history unavailable", "error", e)
	}
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

// ResolveTelegramConfigPath returns the shell3.lua the Telegram host should load.
// Order (telegram-only; do not reorder): the explicit flag, else the dedicated
// telegram config ~/.shell3/telegram/shell3.lua, else the legacy global
// ~/.shell3/shell3.lua (so an existing setup keeps working), else a project-local
// ./shell3.lua. This deliberately differs from ResolveConfigPath, which the TUI
// and other front-ends keep using (project-local first).
func ResolveTelegramConfigPath(flag, cwd, homeDir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	telegram := filepath.Join(homeDir, ".shell3", "telegram", "shell3.lua")
	if fileExists(telegram) {
		return telegram, nil
	}
	global := filepath.Join(homeDir, ".shell3", "shell3.lua")
	if fileExists(global) {
		return global, nil
	}
	local := filepath.Join(cwd, "shell3.lua")
	if fileExists(local) {
		return local, nil
	}
	return "", fmt.Errorf("no shell3.lua found — run 'shell3 boot --telegram' to create one (or pass --config)")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
