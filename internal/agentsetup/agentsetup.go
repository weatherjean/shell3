// Package agentsetup is the shared config assembly used by every shell3
// front-end (`shell3 telegram`, `shell3 serve`, `shell3 ask`, and the internal/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads the config directory, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"strings"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/mcp"
	"github.com/weatherjean/shell3/internal/mediadir"
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
	ConfigDir string // "" triggers default resolution (ResolveConfigDir)
	CWD       string
	HomeDir   string
}

// Parts is the session-independent runtime assembly: everything one process
// shares across N sessions. Front-ends derive per-session chat.Configs from it
// via SessionConfig.
//
// Concurrency: all exported methods are safe for concurrent use by multiple
// sessions. The loaded config (config.LoadedConfig) is immutable after load,
// the runs store serializes writes through its database handle (safe for
// concurrent callers), the proxy spawner is mutex-guarded internally,
// and AgentRuntime builds a fresh LLM client per call, so no client state is
// shared across sessions.
//
// Lifetime: Parts must not be used after the cleanup returned by BuildParts has
// run. The cleanup closes MCP connections, the runs store's database handle,
// and the log; run_proxy processes are detached (never reaped here).
// Any method call after cleanup has undefined behaviour.
type Parts struct {
	lc      *config.LoadedConfig
	st      *runs.Store
	proxy   *modelproxy.Spawner
	log     applog.Logger
	root    string // runtime root workdir (Options.CWD)
	runsDir string // absolute path to .shell3_project/runs (for chat.Config.RunsDir + the Environment section)
	// configDir is the resolved absolute config directory that produced this Parts;
	// recorded per session so resume can reload the right config.
	configDir string
	// mcp is the connected MCP server manager (nil when no mcp: block
	// is declared). Its Close rides the BuildParts closer stack, so /reload
	// tears down old servers and connects fresh ones automatically. mcpWarns
	// holds connect-time warnings (down servers, tool-name collisions),
	// surfaced beside the config warnings.
	mcp      *mcp.Manager
	mcpWarns []string
	// kit is the parsed kit file when one was loaded (see LoadKit); nil
	// otherwise. Agents declared in it resolve through KitAgentRuntime.
	kit     *kit.Kit
	kitPath string
	// home is the user's home dir, for expanding ~/ in a kit agent's workdir.
	home string
}

// MCPStatus reports every declared MCP server's health (nil when no
// mcp: block is declared) — for `shell3 health` and /status.
func (p *Parts) MCPStatus() []mcp.ServerStatus {
	if p.mcp == nil {
		return nil
	}
	return p.mcp.Status()
}

// Store returns the runs store (always opened; nil only when the
// store-open itself failed, which is non-fatal and logged).
func (p *Parts) Store() *runs.Store { return p.st }

// LoadedConfig exposes the parsed config, including the hooks a kit's `gate:`
// and `note:` blocks installed. Front-ends reach it through the higher-level
// accessors below; this is for callers that need the hook surface itself.
func (p *Parts) LoadedConfig() *config.LoadedConfig { return p.lc }

// Log returns the application logger (never nil once BuildParts succeeded).
func (p *Parts) Log() applog.Logger { return p.log }

// ConfigDir returns the resolved absolute config directory that produced these
// parts (recorded per session for resume).
func (p *Parts) ConfigDir() string { return p.configDir }

// BackgroundMaxConcurrent returns the `background.max_concurrent`
// setting (0 = unset; default applied at newJobManager).
func (p *Parts) BackgroundMaxConcurrent() int { return p.lc.BackgroundMaxConcurrent }

// ModelCount returns the number of declared models.
func (p *Parts) ModelCount() int { return len(p.lc.Models) }

// Telegram returns the parsed telegram: block (zero value if absent).
func (p *Parts) Telegram() config.TelegramConfig { return p.lc.Telegram() }

// Cron returns the jobs declared as cron/<name>.md files.
func (p *Parts) Cron() []config.CronJob { return p.lc.Cron() }

// RunsKeepDays returns `runs_keep_days` (always populated at load — default
// 30; 0 = keep forever). Read by the runs janitor at `shell3 telegram` startup.
func (p *Parts) RunsKeepDays() int { return p.lc.RunsKeepDays }

// MediaKeepDays returns `media_keep_days` (always populated at load —
// default 0 = keep forever). Read by the media janitor at `shell3 telegram`
// startup.
func (p *Parts) MediaKeepDays() int { return p.lc.MediaKeepDays }

// RunsRoot returns the .shell3_project directory the runs Store was opened
// against (runs.Open's root param) — the same root the runs janitor's Sweep
// expects. Derived from runsDir (.../.shell3_project/runs) rather than
// storing it separately, since Store already keys off exactly this
// relationship (see runs.Store.runsDir).
func (p *Parts) RunsRoot() string { return filepath.Dir(p.runsDir) }

// AgentNames returns declared agent names in declaration order.
func (p *Parts) AgentNames() []string {
	agents := p.lc.Agents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// Subagents returns the registered subagents (name, description, model) in
// filename order — what a front-end needs to show the delegation roster.
func (p *Parts) Subagents() []config.Subagent { return p.lc.Subagents() }

// Skills returns the main agent's skill index (subagents carry none).
func (p *Parts) Skills() []config.Skill { return p.lc.FirstAgent().Skills }

// AgentRuntime assembles the full chat runtime for the named agent: its model
// client, persona, and tool defs. name "" uses the first declared agent. An
// unknown non-empty name falls back to the subagent registry (so a subagent
// spawned by name — via the task tool or a cron job — resolves the headless
// subagent config); a name in neither registry returns an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	// A loaded kit owns every agent it declares: kit agents take precedence
	// over the markdown config so a migrated install runs on the kit alone.
	if p.kit != nil {
		if rt, err := p.KitAgentRuntime(name); err == nil {
			return rt, nil
		} else if !errors.Is(err, errNoSuchKitAgent) {
			return chat.ActiveAgent{}, err
		}
	}
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

// SubagentWorkdir returns the declared working directory for a subagent —
// non-empty only for project managers, whose shell runs in the project's
// workdir. "" means "inherit the spawner's workdir" (the default for
// ordinary subagents).
func (p *Parts) SubagentWorkdir(name string) string {
	if sa, ok := p.lc.SubagentByName(name); ok {
		return sa.Workdir
	}
	return ""
}

// subagentToAgent adapts a registered subagent to the config.Agent shape that
// runtimeForAgent/BuildPersonaFor consume. The shared core copies wholesale;
// Subagents stays empty (delegation is single-level by construction) and the
// model-facing Description is dropped (it matters to the parent, not here).
func subagentToAgent(sa config.Subagent) config.Agent {
	return config.Agent{AgentCommon: sa.AgentCommon}
}

// runtimeForAgent assembles the full chat runtime for the given agent value.
// It is the common implementation shared by the agent and subagent resolution
// paths in AgentRuntime.
func (p *Parts) runtimeForAgent(a config.Agent) (chat.ActiveAgent, error) {
	md, ok := p.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	toolDefs := config.ToolDefs(a.Gates)

	// Inject the `task` tool when the agent has a non-empty Subagents list —
	// delegation is inferred from agents/ being non-empty, there is no toggle.
	// The allowlist (names + model-facing descriptions) is baked into the
	// tool's schema — subagent_type carries an enum plus a per-subagent
	// description — so the model needs no separate delegation reminder.
	// a.Subagents is also surfaced via ActiveAgent.Subagents below so the
	// Session can validate spawns.
	if len(a.Subagents) > 0 {
		refs := make([]config.SubagentRef, 0, len(a.Subagents))
		for _, n := range a.Subagents {
			desc := ""
			if sa, ok := p.lc.SubagentByName(n); ok {
				desc = sa.Description
			}
			refs = append(refs, config.SubagentRef{Name: n, Description: desc})
		}
		toolDefs = append(toolDefs, config.TaskToolFor(refs), config.TaskListTool, config.TaskStatusTool, config.TaskCancelTool)
	}

	// Append the opted-in MCP servers' tool defs (the mcp: frontmatter) and route their
	// names to the host-tool dispatcher. The map is fresh per call: session
	// RegisterHostTool (send_media_telegram etc.) mutates it later.
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

	// ActiveSkills is the display list (/status): resolved
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
			Environment:   true,
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
// and this session's id, and where conversation history lives — the SQLite
// runs store the history tool searches. The result is wrapped in
// <system-reminder>…</system-reminder>.
//
// It is a package-level function (not a *Parts method) so internal/shell3 can render
// it from the per-session chat.Config fields it already holds — config path,
// runs dir, model (from the status line), and the runs session id — keeping the
// fact wording in exactly one place.
//
// Returns "" when runsDir is empty (store-open failed), so the reminder never
// advertises a path the agent cannot use.
func EnvironmentReminder(configDir, runsDir, model, sessionID string) string {
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
	if configDir != "" {
		// Name what is actually on disk. A reminder that lists files the
		// install does not have teaches the model a layout it will then
		// contradict itself about.
		layout := "shell3.sh (agents + tools), skills/, projects/<agent>/skills/, hooks/"
		if !fileExists(filepath.Join(configDir, KitFileName)) {
			layout = "shell3.yaml, agent.md, skills/, hooks/"
		}
		fmt.Fprintf(&b, "- config: `%s` (your config directory: %s — edit it via the self-evolve skill)\n", configDir, layout)
	}
	// Derive the model-facing paths from paths.ProjectDirName (its single
	// source): a renamed project dir must not leave the reminder teaching the
	// model paths that no longer exist.
	fmt.Fprintf(&b, "- history: every conversation (subagent runs included) is stored in `%s/shell3.db`; recall past sessions with the history tool when you have it (search, then read around a hit)\n", paths.ProjectDirName)
	fmt.Fprintf(&b, "- background job logs: `%s/runs/<session>/jobs/<job>.log` (plain files)\n", paths.ProjectDirName)
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

// BridgeVerdict maps a config tool-call hook verdict to the chat package's
// equivalent, field by field. The two Action enums are independent iota
// blocks; an explicit mapping (rather than a numeric cast) keeps this security
// boundary correct if either is ever reordered, and an unrecognized action
// fails closed (ActionBlock) rather than silently falling through to
// ActionRun. Exported so integration tests exercise the same bridge
// production uses instead of hand-copying it.
func BridgeVerdict(v config.ToolCallVerdict) chat.ToolCallVerdict {
	action := chat.ActionBlock // fail closed on any unmapped action
	if v.Action == config.ActionRun {
		action = chat.ActionRun
	}
	return chat.ToolCallVerdict{
		Action:      action,
		Argv:        v.Argv,
		Reason:      v.Reason,
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
		ConfigDir:      p.ConfigDir(),
		ConfigWarnings: append(append([]string{}, p.lc.Warnings()...), p.mcpWarns...),
		Log:            p.log,
		OutPath:        so.OutPath,
		Headless:       so.Headless,
		AgentNames:     p.AgentNames(),
		RefreshPrompt:  func() string { return p.RefreshPromptFor(activeName) },
		// Agent-scoped knobs (Environment, Delegation, thresholds, …) arrive via
		// cfg.ApplyActiveAgent(rt) below.
	}
	// Kit tools: declared verbs dispatch to their shell functions. Composed
	// under MCP so a kit tool and an MCP tool never shadow each other silently.
	var kitTool func(context.Context, string, string) (string, error)
	if p.kit != nil {
		if r, err := p.KitAgent(so.Agent); err == nil {
			kitTool = p.KitHostTool(r, workdir)
		}
	}
	// MCP dispatch: the base of the session's host-tool chain. Session-level
	// RegisterHostTool calls (the bot's send_media_telegram/status/reload)
	// compose on top of it, falling through here for names they don't own; unowned names
	// end in the chat layer's unknown-tool handling via ErrHostToolNotFound.
	if mgr := p.mcp; mgr != nil {
		cfg.HostTool = func(ctx context.Context, name, argsJSON string) (string, error) {
			if mgr.Owns(name) {
				return mgr.Call(ctx, name, argsJSON)
			}
			if kitTool != nil {
				return kitTool(ctx, name, argsJSON)
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
	if cfg.HostTool == nil && kitTool != nil {
		cfg.HostTool = kitTool
	}
	// hooks/*.tool-call.sh: the per-agent gate script run before every tool.
	// The closures capture activeName so a /agent switch re-targets the
	// session to the new agent's hook (each agent is governed by its own
	// script or none — no fallback).
	if p.lc.HasToolCall() {
		cfg.RunToolCall = func(ctx context.Context, name, command, argsJSON string, headless bool) chat.ToolCallVerdict {
			return BridgeVerdict(p.lc.RunToolCall(ctx, activeName, name, command, argsJSON, headless))
		}
	}
	// hooks/*.tool-result.sh: the per-agent output-rewrite script.
	if p.lc.HasToolResult() {
		cfg.RunToolResult = func(ctx context.Context, name, argsJSON, output string) string {
			return p.lc.RunToolResult(ctx, activeName, name, argsJSON, output)
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
// MCP connections, the runs store, and the log; callers MUST invoke it once.
// (run_proxy processes are detached fire-and-forget — see modelproxy.)
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
		configDir: b.configDir,
		mcp:       b.mcp, mcpWarns: b.mcpWarns,
		home: opts.HomeDir,
	}
	// A shell3.sh beside the config is THE config: its agents, tools, and
	// skills take precedence over the markdown tree. Presence enables it —
	// there is no toggle.
	if kp := filepath.Join(b.configDir, KitFileName); fileExists(kp) {
		if err := p.LoadKit(kp); err != nil {
			b.closeAll()
			return nil, noop, err
		}
	}
	return p, b.closeAll, nil
}

// KitFileName is the kit a config directory is read from when present.
const KitFileName = "shell3.sh"

// builder accumulates the state and open resources used to assemble the shared
// Parts across BuildParts' stages. closers is a LIFO teardown stack: stages
// push a closer as they acquire a resource, and closeAll runs them in
// reverse-acquisition order — matching the original cleanup ordering
// (store → lc → log).
type builder struct {
	opts Options

	configDir string
	g         paths.Global
	l         paths.Local

	log      applog.Logger
	lc       *config.LoadedConfig
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
	configDir, err := ResolveConfigDir(b.opts.ConfigDir, b.opts.HomeDir)
	if err != nil {
		return err
	}
	b.configDir = configDir
	mediadir.SetBaseDir(configDir)
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

// loadConfig loads the config directory. Hooks and .env resolve against that
// directory; the agent's bash cwd stays opts.CWD. These differ on purpose.
func (b *builder) loadConfig() error {
	lc, err := config.Load(b.configDir)
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

// connectMCP builds + connects the MCP server manager when an mcp: block is
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

// openStore opens the runs store (the per-project SQLite database)
// unconditionally: it always persists the conversation, and the history tool
// reads it back. Non-fatal: a failure warns and proceeds with a nil store
// (persistence and history silently degrade). The database handle rides the
// closer stack so a reload's parked old generation releases it.
func (b *builder) openStore() {
	if s, e := runs.Open(b.l.Root); e == nil {
		b.st = s
		b.closers = append(b.closers, func() { _ = s.Close() })
	} else {
		b.log.Warn("open store failed — history unavailable", "error", e)
		fmt.Fprintln(os.Stderr, "shell3: warning: open store failed — conversations will not persist and history is unavailable: "+e.Error())
	}
}

// buildClient constructs a streaming client plus its request params from a
// configured model. Reused for the initial client and on each agent switch.
func buildClient(md config.Model) (chat.LLMClient, llm.RequestParams) {
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

// ResolveConfigDir returns the config directory to load: the explicit flag (a
// literal directory path), else the default ~/.shell3. It does NOT look in
// cwd. Returns an error when the resolved directory has no shell3.yaml —
// catching a typo'd --config here, with a clear message, instead of surfacing
// it later as a raw load error.
func ResolveConfigDir(flag, homeDir string) (string, error) {
	dir := flag
	if dir == "" {
		dir = paths.NewGlobal(homeDir).Root
	}
	// A kit is a complete config on its own — it carries its wiring in a
	// `shell3:` block — so either file makes the directory valid.
	if fileExists(filepath.Join(dir, KitFileName)) || fileExists(filepath.Join(dir, "shell3.yaml")) {
		return dir, nil
	}
	return "", fmt.Errorf("no %s or shell3.yaml in %s — run 'shell3 boot' to create one (or pass --config <dir>)", KitFileName, dir)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
