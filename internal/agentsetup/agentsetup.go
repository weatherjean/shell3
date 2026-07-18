// Package agentsetup is the shared config assembly used by every shell3
// front-end (the Telegram bot, the dev CLIs, and the internal/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads shell3.lua, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"context"
	"fmt"
	"os"

	"strings"

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
	"github.com/weatherjean/shell3/internal/runs"
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
// sessions. The Lua VM (luacfg.LoadedConfig) serialises access with a mutex,
// the file-native runs store appends each line with a single O_APPEND write
// (safe for concurrent callers), the proxy spawner is mutex-guarded internally,
// and AgentRuntime builds a fresh LLM client per call, so no client state is
// shared across sessions.
//
// Lifetime: Parts must not be used after the cleanup returned by BuildParts has
// run. The cleanup closes the Lua state and the log; the runs store has no
// handle to close and run_proxy processes are detached (never reaped here).
// Any method call after cleanup has undefined behaviour.
type Parts struct {
	lc      *luacfg.LoadedConfig
	st      *runs.Store
	proxy   *modelproxy.Spawner
	log     applog.Logger
	root    string // runtime root workdir (Options.CWD)
	runsDir string // absolute path to .shell3_project/runs (for chat.Config.RunsDir + the Environment section)
	// configPath is the resolved absolute shell3.lua that produced this Parts;
	// recorded per session so resume can reload the right config.
	configPath string
	// mcp is the connected MCP server manager (nil when no shell3.mcp{} block
	// is declared). Its Close rides the BuildParts closer stack, so /reload
	// tears down old servers and connects fresh ones automatically. mcpWarns
	// holds connect-time warnings (down servers, tool-name collisions),
	// surfaced beside the Lua config warnings.
	mcp      *mcp.Manager
	mcpWarns []string
}

// MCPStatus reports every declared MCP server's health (nil when no
// shell3.mcp{} block is declared) — for `shell3 health` and the dashboard.
func (p *Parts) MCPStatus() []mcp.ServerStatus {
	if p.mcp == nil {
		return nil
	}
	return p.mcp.Status()
}

// Store returns the file-native runs store (always opened; nil only when the
// store-open itself failed, which is non-fatal and logged).
func (p *Parts) Store() *runs.Store { return p.st }

// ConfigPath returns the resolved absolute shell3.lua path that produced these
// parts (recorded per session for resume).
func (p *Parts) ConfigPath() string { return p.configPath }

// MediaConfig is the read-only slice of shell3.lua that internal/media needs
// to resolve its four capabilities (STT/TTS/Describe/Imagegen) and their
// models. It mirrors media.Config's method set so *luacfg.LoadedConfig
// satisfies both structurally, without agentsetup importing media (media
// cannot live under agentsetup itself, since it depends on internal/shell3,
// which agentsetup is built from) or media importing agentsetup.
type MediaConfig interface {
	STT() *luacfg.STTConfig
	TTS() *luacfg.TTSConfig
	Describe() *luacfg.DescribeConfig
	Imagegen() *luacfg.ImagegenConfig
	Model(name string) (luacfg.Model, bool)
}

// MediaConfig returns the narrow media-config view of the shell3.lua this
// Parts was built from, for host code building media.Clients (e.g.
// media.New(p.MediaConfig(), p.EnsureProxy)).
func (p *Parts) MediaConfig() MediaConfig { return p.lc }

// EnsureProxy starts (or no-ops if already running) the run_proxy command for
// a named model, mirroring AgentRuntime's own proxy-spawn call. Exposed as a
// pass-through so host code can pass it directly as the ensureProxy func
// media.New expects, without reaching into the unexported proxy field.
func (p *Parts) EnsureProxy(name, command string) { p.proxy.Ensure(name, command) }

// BackgroundMaxConcurrent returns the shell3.background{ max_concurrent = N }
// setting (0 = unset; default applied at newJobManager).
func (p *Parts) BackgroundMaxConcurrent() int { return p.lc.BackgroundMaxConcurrent }

// ModelCount returns the number of declared models.
func (p *Parts) ModelCount() int { return len(p.lc.Models) }

// Telegram returns the parsed shell3.telegram{} block (zero value if absent).
func (p *Parts) Telegram() luacfg.TelegramConfig { return p.lc.Telegram() }

// Web returns the parsed top-level shell3.web{} block (zero value if absent).
func (p *Parts) Web() luacfg.WebConfig { return p.lc.Web() }

// Cron returns the cron jobs declared via top-level shell3.cron{...}.
func (p *Parts) Cron() []luacfg.CronJob { return p.lc.Cron() }

// Heartbeat returns the parsed shell3.heartbeat{} block, nil when not declared.
func (p *Parts) Heartbeat() *luacfg.Heartbeat { return p.lc.Heartbeat() }

// AgentNames returns declared agent names in declaration order.
func (p *Parts) AgentNames() []string {
	agents := p.lc.Agents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// AgentRuntime assembles the full chat runtime for the named agent: its model
// client, persona, and tool defs. name "" uses the first declared agent. An
// unknown non-empty name falls back to the subagent registry (so a subagent
// spawned by name — via the task tool or a cron job — resolves the headless
// subagent config); a name in neither registry returns an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	if name == "" {
		return p.runtimeForAgent(p.lc.FirstAgent())
	}
	if a, ok := p.lc.AgentByName(name); ok {
		return p.runtimeForAgent(a)
	}
	// A subagent name passed via --agent (the spawn command): resolve it from the
	// subagent registry into a plain headless config. Whether a resolved agent
	// gets the task tool is decided by whether it lists subagents, not by a
	// spawn-time flag.
	if sa, ok := p.lc.SubagentByName(name); ok {
		return p.runtimeForAgent(subagentToAgent(sa))
	}
	return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
}

// subagentToAgent adapts a registered subagent to the luacfg.Agent shape that
// runtimeForAgent/BuildPersonaFor consume. The shared core copies wholesale;
// Subagents stays empty (delegation is single-level by construction) and the
// model-facing Description is dropped (it matters to the parent, not here).
func subagentToAgent(sa luacfg.Subagent) luacfg.Agent {
	return luacfg.Agent{AgentCommon: sa.AgentCommon}
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

	toolDefs := luacfg.ToolDefs(a.Gates)

	// Inject the `task` tool when the agent has both the Delegation toggle and a
	// non-empty Subagents allowlist. The allowlist (names + model-facing
	// descriptions) is baked into the tool's schema — subagent_type carries an
	// enum plus a per-subagent description — so the model needs no separate
	// delegation reminder. a.Subagents is also surfaced via
	// ActiveAgent.Subagents below so the Session can validate spawns.
	if a.Delegation && len(a.Subagents) > 0 {
		refs := make([]luacfg.SubagentRef, 0, len(a.Subagents))
		for _, n := range a.Subagents {
			desc := ""
			if sa, ok := p.lc.SubagentByName(n); ok {
				desc = sa.Description
			}
			refs = append(refs, luacfg.SubagentRef{Name: n, Description: desc})
		}
		toolDefs = append(toolDefs, luacfg.TaskToolFor(refs), luacfg.TaskListTool, luacfg.TaskStatusTool, luacfg.TaskCancelTool)
	}

	// Append the opted-in MCP servers' tool defs (tools.mcp) and route their
	// names to the host-tool dispatcher. The map is fresh per call: session
	// RegisterHostTool (image_generate etc.) mutates it later.
	var hostNames map[string]bool
	if p.mcp != nil && (a.MCPAll || len(a.MCP) > 0) {
		mcpDefs := p.mcp.Tools(a.MCP, a.MCPAll)
		if len(mcpDefs) > 0 {
			toolDefs = append(toolDefs, mcpDefs...)
			hostNames = make(map[string]bool, len(mcpDefs))
			for _, d := range mcpDefs {
				hostNames[d.Name] = true
			}
		}
	}

	prompt := p.lc.BuildPersonaFor(a)

	// toolNames is exactly toolDefs' names — derived once at the end so the
	// two can never skew.
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	// ActiveSkills is the display list (status tool, dashboard): resolved
	// skill names in index order.
	skillNames := make([]string, 0, len(a.Skills))
	for _, s := range a.Skills {
		skillNames = append(skillNames, s.Name)
	}

	// prune=false zeroes the effective prune threshold for this agent/subagent;
	// PruneAt=0 is already the disabled state downstream (chat.maybeCompact).
	// nil/true inherit the model's prune_at — the flag can gate the stage but
	// never invent a threshold the model doesn't declare.
	pruneAt := md.PruneAt
	if a.Prune != nil && !*a.Prune {
		pruneAt = 0
	}

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: prompt,
			Tools:        toolDefs,
		},
		ModeLabel:    a.Name,
		ActiveSkills: skillNames,
		ActiveTools:  toolNames,
		LLM:          client,
		Params:       rp,
		ModelID:      md.ModelID,
		AgentKnobs: chat.AgentKnobs{
			HostToolNames: hostNames,
			Subagents:     a.Subagents,
			Environment:   a.Environment,
			ContextWindow: md.ContextWindow,
			CompactAt:     md.CompactAt,
			KeepRecent:    md.KeepRecent,
			PruneAt:       pruneAt,
		},
	}, nil
}

// EnvironmentReminder renders the host-injected Environment standing reminder
// (no longer part of the system prompt). It exposes the agent's own config path
// (so any front-end can resolve its config dir without a tool), the active model
// and this session's id, and where conversation history lives on disk — all
// file-native, searchable with ordinary Unix tools (rg/grep/cat). The result is wrapped in <system-reminder>…</system-reminder>.
//
// It is a package-level function (not a *Parts method) so internal/shell3 can render
// it from the per-session chat.Config fields it already holds — config path,
// runs dir, model (from the status line), and the runs session id — keeping the
// fact wording in exactly one place.
//
// Returns "" when runsDir is empty (store-open failed), so the reminder never
// advertises a path the agent cannot use.
func EnvironmentReminder(configPath, runsDir, model, sessionID string) string {
	if runsDir == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\nEnvironment (read-only unless stated):\n")
	if model != "" {
		fmt.Fprintf(&b, "- model: %s\n", model)
	}
	if sessionID != "" {
		fmt.Fprintf(&b, "- session id: %s\n", sessionID)
	}
	if configPath != "" {
		fmt.Fprintf(&b, "- config: `%s` (your shell3.lua; its directory holds your skills/lib — edit it via the self-evolve skill)\n", configPath)
	}
	// Derive the model-facing paths from paths.ProjectDirName (its single
	// source): a renamed project dir must not leave the reminder teaching the
	// model paths that no longer exist.
	relRuns := paths.ProjectDirName + "/runs"
	fmt.Fprintf(&b, "- history: every conversation is verbatim JSONL at `%s/<id>/messages.jsonl` (one message per line; `meta.json` beside it holds model/status/timestamps)\n", relRuns)
	fmt.Fprintf(&b, "- search history: `rg <terms> %s` (ordinary ripgrep over the JSONL — no special CLI)\n", relRuns)
	fmt.Fprintf(&b, "- subagent transcripts are ordinary sessions under `%s` too (one dir per child session)\n", relRuns)
	b.WriteString("</system-reminder>")
	return b.String()
}

// RefreshPromptFor re-renders the named agent's or subagent's system prompt
// (used by /clear). name may be a declared agent name or a registered subagent
// name; callers pass names already validated by a successful AgentRuntime call
// (names come from ModeLabel, set to a.Name only on a successful lookup). The
// FirstAgent fallback exists only so an impossible miss degrades to a sane
// prompt rather than panicking; in correct use that branch is never reached.
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
	Agent    string // "" → first declared (falls back to a subagent name)
	WorkDir  string // "" → runtime root
	Headless bool
	OutPath  string
}

// BridgeVerdict maps a luacfg on_tool_call verdict to the chat package's
// equivalent, field by field. The two Action enums are independent iota
// blocks; an explicit mapping (rather than a numeric cast) keeps this security
// boundary correct if either is ever reordered, and an unrecognized action
// fails closed (ActionBlock) rather than silently falling through to
// ActionRun. Exported so integration tests exercise the same bridge
// production uses instead of hand-copying it.
func BridgeVerdict(v luacfg.ToolCallVerdict) chat.ToolCallVerdict {
	action := chat.ActionBlock // fail closed on any unmapped action
	switch v.Action {
	case luacfg.ActionRun:
		action = chat.ActionRun
	case luacfg.ActionAsk:
		action = chat.ActionAsk
	}
	return chat.ToolCallVerdict{
		Action:      action,
		Argv:        v.Argv,
		Prompt:      v.Prompt,
		Reason:      v.Reason,
		AskTimeout:  v.AskTimeout,
		Passthrough: v.Passthrough,
	}
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
	// below; internal/shell3.Session.SwitchAgent is documented single-threaded
	// (between turns), so a plain pointer is sufficient.
	activeName := rt.ModeLabel
	cfg := chat.Config{
		Store:          p.st,
		RunsDir:        p.runsDir,
		WorkDir:        workdir,
		ConfigPath:     p.ConfigPath(),
		ConfigWarnings: append(append([]string{}, p.lc.Warnings()...), p.mcpWarns...),
		Log:            p.log,
		OutPath:        so.OutPath,
		Headless:       so.Headless,
		AgentNames:     p.AgentNames(),
		RefreshPrompt:  func() string { return p.RefreshPromptFor(activeName) },
		// Agent-scoped knobs (Environment, Delegation, thresholds, …) arrive via
		// cfg.ApplyActiveAgent(rt) below.
	}
	// MCP dispatch: the base of the session's host-tool chain. Session-level
	// RegisterHostTool calls (image_generate, the bot's send/status) compose on
	// top of it, falling through here for names they don't own; unowned names
	// end in the chat layer's unknown-tool handling via ErrHostToolNotFound.
	if mgr := p.mcp; mgr != nil {
		cfg.HostTool = func(ctx context.Context, name, argsJSON string) (string, error) {
			if mgr.Owns(name) {
				return mgr.Call(ctx, name, argsJSON)
			}
			return "", fmt.Errorf("%w: %q", chat.ErrHostToolNotFound, name)
		}
		cfg.MCPStatus = func() []chat.MCPServerStatus {
			sts := mgr.Status()
			out := make([]chat.MCPServerStatus, 0, len(sts))
			for _, st := range sts {
				out = append(out, chat.MCPServerStatus{Name: st.Name, Up: st.Up, ToolCount: st.ToolCount, Err: st.Err})
			}
			return out
		}
	}
	// shell3.on_tool_call: config-global hook chain run before every tool.
	if p.lc.HasToolCall() {
		cfg.RunToolCall = func(ctx context.Context, name, command, argsJSON string, headless bool) chat.ToolCallVerdict {
			return BridgeVerdict(p.lc.RunToolCall(ctx, name, command, argsJSON, headless))
		}
	}
	// shell3.on_tool_result: config-global output-rewrite chain.
	if p.lc.HasToolResult() {
		cfg.RunToolResult = func(ctx context.Context, name, argsJSON, output string) string {
			return p.lc.RunToolResult(ctx, name, argsJSON, output)
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
// the Lua state and the log; callers MUST invoke it once. (The runs store has
// no handle to close, and run_proxy processes are detached fire-and-forget —
// see openStore and modelproxy.)
func BuildParts(opts Options) (*Parts, func(), error) {
	b := &builder{opts: opts}
	noop := func() {}
	if err := b.resolvePaths(); err != nil {
		return nil, noop, err
	}
	b.openLog()
	b.proxy = modelproxy.New(b.g.Root, b.log)
	if err := b.loadConfig(); err != nil {
		b.closeAll()
		return nil, noop, err
	}
	b.connectMCP()
	b.openStore()
	p := &Parts{lc: b.lc, st: b.st, proxy: b.proxy,
		log: b.log, root: b.opts.CWD, runsDir: b.l.Runs,
		configPath: b.configPath,
		mcp:        b.mcp, mcpWarns: b.mcpWarns,
	}
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

	log      applog.Logger
	lc       *luacfg.LoadedConfig
	st       *runs.Store
	proxy    *modelproxy.Spawner
	mcp      *mcp.Manager
	mcpWarns []string

	closers []func() // LIFO teardown stack
}

// closeAll runs the teardown stack in reverse-acquisition order.
func (b *builder) closeAll() {
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}

// resolvePaths resolves the config path, builds the global/local path sets, and
// ensures the global root + project runtime directories exist. The project
// identity is now the directory itself (.shell3_project/), so there is no UUID.
func (b *builder) resolvePaths() error {
	configPath, err := ResolveConfigPath(b.opts.ConfigPath, b.opts.HomeDir)
	if err != nil {
		return err
	}
	b.configPath = configPath
	b.g = paths.NewGlobal(b.opts.HomeDir)
	b.l = paths.NewLocal(b.opts.CWD)
	if err := bootstrap.EnsureGlobal(b.g); err != nil {
		return err
	}
	if err := bootstrap.EnsureProject(b.l); err != nil {
		return err
	}
	return nil
}

// openLog opens the rotating app log. Failure is non-fatal: it warns on stderr
// (the log itself being unavailable to record it) and falls back to Noop.
func (b *builder) openLog() {
	log, logCloser, err := applog.Open(b.g.LogFile, applog.DefaultMaxBytes, applog.DefaultMaxArchives)
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
	lc, err := luacfg.Load(b.configPath)
	if err != nil {
		return err
	}
	b.lc = lc
	// Surface non-fatal config issues (e.g. a skipped invalid skill file). To
	// both the app log and stderr: the log keeps a durable record, and stderr
	// reaches headless/CLI runs directly.
	for _, w := range lc.Warnings() {
		b.log.Warn("config warning", "detail", w)
		fmt.Fprintln(os.Stderr, "shell3: config warning: "+w)
	}
	b.closers = append(b.closers, func() { lc.Close() })
	return nil
}

// connectMCP builds + connects the MCP server manager when shell3.mcp{} is
// declared. Synchronous by design (servers dial in parallel, each under its
// own timeout): tool defs become plain static config, and a hosted bot does
// not care about a few seconds at startup/reload. A down server is a warning,
// never a build failure; its tools are absent until the next reload.
func (b *builder) connectMCP() {
	servers := b.lc.MCPServers()
	if len(servers) == 0 {
		return
	}
	b.mcp = mcp.New(servers, b.log)
	b.closers = append(b.closers, b.mcp.Close)
	b.mcpWarns = b.mcp.Connect(context.Background())
	for _, w := range b.mcpWarns {
		b.log.Warn("mcp warning", "detail", w)
		fmt.Fprintln(os.Stderr, "shell3: mcp warning: "+w)
	}
}

// openStore opens the file-native runs store unconditionally: it always persists
// the conversation (saveHistory) and the agent reads it back out-of-process with
// rg/cat over the JSONL. Non-fatal: a failure warns and proceeds with a nil store
// (persistence and history reads silently degrade). The store has no handle to
// close — runs.Store is stateless over the filesystem.
func (b *builder) openStore() {
	if s, e := runs.Open(b.l.Root); e == nil {
		b.st = s
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

// ExpandConfigName turns a raw --config flag value into a config path. A value
// ending in ".lua" is a literal path (returned unchanged); any other non-empty
// value is a name resolved to ~/.shell3/<name>.lua; an empty value is returned
// as-is (the caller applies its own default). This is the single rule every
// front-end and CLI uses so name-vs-path resolution stays consistent.
func ExpandConfigName(flag, homeDir string) string {
	if flag == "" || strings.HasSuffix(flag, ".lua") {
		return flag
	}
	return paths.NewGlobal(homeDir).ConfigNamed(flag)
}

// ResolveConfigPath returns the shell3.lua to load: the explicit flag (a name
// like "code" → ~/.shell3/code.lua, or a literal *.lua path), else the default
// ~/.shell3/shell3.lua. It does NOT look in cwd. Returns an error when the
// resolved file does not exist — catching a typo'd --config here, with a clear
// message, instead of surfacing it later as a raw Lua DoFile error.
func ResolveConfigPath(flag, homeDir string) (string, error) {
	if expanded := ExpandConfigName(flag, homeDir); expanded != "" {
		if !fileExists(expanded) {
			return "", fmt.Errorf("no such config: %s — run 'shell3 boot' to create one, or check the --config name", expanded)
		}
		return expanded, nil
	}
	global := paths.NewGlobal(homeDir).ConfigFile
	if fileExists(global) {
		return global, nil
	}
	return "", fmt.Errorf("no shell3.lua found — run 'shell3 boot' to create one (or pass --config)")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
