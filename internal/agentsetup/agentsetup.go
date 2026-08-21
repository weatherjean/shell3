// Package agentsetup is the shared config assembly used by every shell3
// front-end (`shell3 telegram`, `shell3 serve`, `shell3 ask`, and the internal/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads the config directory, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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
	"github.com/weatherjean/shell3/internal/review"
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
	// reviewer resolves the gate's {review} verdicts (built lazily by
	// Reviewer(); nil when its model cannot resolve — reviews then fail
	// closed at the chat layer). One instance per Parts so the denial
	// breaker's per-agent tallies span every session of this generation.
	reviewer   *review.Reviewer
	reviewOnce sync.Once
	// home is the user's home dir, for expanding ~/ in a kit agent's workdir.
	home string
}

// MCPStatus reports every declared MCP server's health (nil when no
// mcp: block is declared) — for `shell3 health` and the status tool.
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

// AgentCount returns the number of agents the kit declares (the main agent
// plus every employee).
func (p *Parts) AgentCount() int {
	if p.kit == nil {
		return 0
	}
	return len(p.kit.Agents)
}

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

// DashPort returns `dash_port` (always populated at load — default 7333;
// 0 = dash disabled). Read by the front-end hosts when wiring the web dash.
func (p *Parts) DashPort() int { return p.lc.DashPort }

// RunsRoot returns the .shell3_project directory the runs Store was opened
// against (runs.Open's root param) — the same root the runs janitor's Sweep
// expects. Derived from runsDir (.../.shell3_project/runs) rather than
// storing it separately, since Store already keys off exactly this
// relationship (see runs.Store.runsDir).
func (p *Parts) RunsRoot() string { return filepath.Dir(p.runsDir) }

// AgentRuntime assembles the full chat runtime for the named agent: its model
// client, persona, and tool defs. name "" uses the kit's first declared
// agent; a name the kit does not declare returns an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	return p.KitAgentRuntime(name)
}

// SubagentWorkdir returns the declared working directory for an employee —
// its kit `workdir:`, with a leading ~/ expanded. "" means "inherit the
// spawner's workdir", the default for an agent that declares none.
func (p *Parts) SubagentWorkdir(name string) string {
	if p.kit == nil {
		return ""
	}
	for _, a := range p.kit.Agents {
		if a.Name == name && a.Workdir != "" {
			return expandHomePath(a.Workdir, p.home)
		}
	}
	return ""
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
		fmt.Fprintf(&b, "- config: `%s` (your config directory: %s (wiring, agents, tools, the gate), skills/, projects/<agent>/skills/ — edit it via the self-evolve skill)\n", configDir, kit.FileName)
	}
	// Derive the model-facing paths from paths.ProjectDirName (its single
	// source): a renamed project dir must not leave the reminder teaching the
	// model paths that no longer exist.
	fmt.Fprintf(&b, "- history: every conversation (subagent runs included) is stored in `%s/shell3.db`; recall past sessions with the history tool when you have it (search, then read around a hit)\n", paths.ProjectDirName)
	fmt.Fprintf(&b, "- background job logs: `%s/runs/<session>/jobs/<job>.log` (plain files)\n", paths.ProjectDirName)
	b.WriteString("</system-reminder>")
	return b.String()
}

// RefreshPromptFor re-renders the named kit agent's system prompt, so a
// `context:` file edited mid-conversation is current on the next turn.
// Callers pass names already validated by a successful AgentRuntime call
// (they come from ModeLabel). An impossible miss returns "" rather than
// panicking; the turn then keeps the prompt it already has.
func (p *Parts) RefreshPromptFor(name string) string {
	r, err := p.KitAgent(name)
	if err != nil {
		return ""
	}
	return p.kitPrompt(r)
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
	switch v.Action {
	case config.ActionRun:
		action = chat.ActionRun
	case config.ActionReview:
		action = chat.ActionReview
	}
	return chat.ToolCallVerdict{
		Action:      action,
		Argv:        v.Argv,
		Reason:      v.Reason,
		Passthrough: v.Passthrough,
	}
}

// SessionConfig derives a per-session chat.Config from the shared parts.
// The returned config embeds per-session closures (RefreshPrompt, the hook
// bridges) that consult only declared config plus the session's own agent.
func (p *Parts) SessionConfig(so SessionOptions) (chat.Config, error) {
	workdir := so.WorkDir
	if workdir == "" {
		workdir = p.root
	}
	rt, err := p.AgentRuntime(so.Agent)
	if err != nil {
		return chat.Config{}, err
	}
	// activeName is the session's agent, captured by the hook closures below.
	// A session's agent is fixed for its lifetime.
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
	// Each agent is governed by its own script or none — no fallback.
	if p.lc.HasToolCall() {
		cfg.RunToolCall = func(ctx context.Context, name, command, argsJSON string, headless bool) chat.ToolCallVerdict {
			return BridgeVerdict(p.lc.RunToolCall(ctx, activeName, name, command, argsJSON, headless))
		}
		// The {review} soft deny resolves through the shared reviewer, keyed
		// by the active agent so one runaway agent's denial-breaker tally
		// never hard-stops another. Nil reviewer (model unresolvable) leaves
		// cfg.ReviewToolCall nil and reviews fail closed downstream.
		if rev := p.Reviewer(); rev != nil {
			cfg.ReviewToolCall = func(ctx context.Context, name, command, reason string) (bool, string) {
				return rev.Review(ctx, activeName, command, reason)
			}
		}
	}
	// hooks/*.tool-result.sh: the per-agent output-rewrite script.
	if p.lc.HasToolResult() {
		cfg.RunToolResult = func(ctx context.Context, name, argsJSON, output string) string {
			return p.lc.RunToolResult(ctx, activeName, name, argsJSON, output)
		}
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
	if kp := filepath.Join(b.configDir, kit.FileName); fileExists(kp) {
		if err := p.LoadKit(kp); err != nil {
			b.closeAll()
			return nil, noop, err
		}
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

// reviewMaxTokens caps the reviewer's reply. The verdict is one word, but a
// reasoning model spends thinking tokens first — 16 (Hermes' cap on plain
// chat models) would truncate the thought and fail every review closed.
const reviewMaxTokens = 1024

// Reviewer returns the shared reviewer behind the gate's {review} verdict,
// built on first use: `review_model` (default: the main agent's model) on a
// DEDICATED client at temperature 0 — never the agent's own client, whose
// params are client state a reviewer override would corrupt. Returns nil
// when the model cannot resolve; the chat layer then fails review verdicts
// closed with a named reason.
func (p *Parts) Reviewer() *review.Reviewer {
	p.reviewOnce.Do(func() {
		name := p.lc.ReviewModel
		if name == "" {
			name = p.defaultModelName()
		}
		md, ok := p.lc.Model(name)
		if !ok {
			p.log.Warn("reviewer model not resolvable — {review} verdicts fail closed", "model", name)
			return
		}
		p.proxy.Ensure(md.Name, md.RunProxy)
		cl := openai.NewClient(md.BaseURL, md.APIKey, md.ModelID)
		zero := 0.0
		cl.SetParams(llm.RequestParams{
			Temperature:     &zero,
			MaxTokens:       reviewMaxTokens,
			ReasoningEffort: md.Reasoning,
		})
		if md.Extra != nil {
			cl.SetExtra(md.Extra)
		}
		p.reviewer = review.New(cl, p.lc.ReviewPolicy)
	})
	return p.reviewer
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
// cwd. Returns an error when the resolved directory has no kit —
// catching a typo'd --config here, with a clear message, instead of surfacing
// it later as a raw load error.
func ResolveConfigDir(flag, homeDir string) (string, error) {
	dir := flag
	if dir == "" {
		dir = paths.NewGlobal(homeDir).Root
	}
	// The kit is the config: it carries its wiring in a `shell3:` block, its
	// agents, its tools and its gate.
	if fileExists(filepath.Join(dir, kit.FileName)) {
		return dir, nil
	}
	return "", fmt.Errorf("no %s in %s — run 'shell3 boot' to create one (or pass --config <dir>)", kit.FileName, dir)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
