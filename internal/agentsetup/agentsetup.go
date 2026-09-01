// Package agentsetup is the config assembly every front-end shares. It
// resolves paths, ensures project dirs, opens the store and log, loads the
// config directory, and returns a chat.Config — the single source of truth for
// what the agent is, independent of how it is driven.
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

// Options tells BuildParts where the config is and which directories the
// runtime resolves against. Per-session concerns live in SessionOptions.
type Options struct {
	ConfigDir string // "" triggers default resolution (ResolveConfigDir)
	CWD       string
	HomeDir   string
}

// Parts is everything one process shares across N sessions; front-ends derive
// per-session chat.Configs from it via SessionConfig.
//
// Every exported method is concurrency-safe: the loaded config is immutable,
// the store serializes writes, the proxy spawner is mutex-guarded, and
// AgentRuntime builds a fresh LLM client per call.
//
// Parts must not be used after BuildParts' cleanup runs — it closes the MCP
// connections, the store handle and the log. run_proxy processes are detached
// and never reaped here.
type Parts struct {
	// events is the shared `event:` delivery worker, started lazily by
	// eventDispatcher() and torn down with these Parts.
	eventOnce sync.Once
	events    *eventDispatcher

	lc      *config.LoadedConfig
	st      *runs.Store
	proxy   *modelproxy.Spawner
	log     applog.Logger
	root    string // runtime root workdir (Options.CWD)
	runsDir string // absolute path to .shell3_project/runs (for chat.Config.RunsDir + the Environment section)
	// configDir produced this Parts, recorded per session so a resume reloads
	// the right config.
	configDir string
	// mcp is the connected server manager, nil when no mcp: block is declared.
	// Its Close rides the closer stack, so /reload reconnects fresh. mcpWarns
	// carries connect-time warnings, shown beside the config ones.
	mcp      *mcp.Manager
	mcpWarns []string
	// kit is the parsed kit file when one was loaded (see LoadKit); nil
	// otherwise. Agents declared in it resolve through KitAgentRuntime.
	kit     *kit.Kit
	kitPath string
	// reviewer resolves the gate's {review} verdicts, built lazily; nil when
	// its model cannot resolve, and reviews then fail closed. One per Parts,
	// so the denial breaker's tallies span every session of this generation.
	reviewer   *review.Reviewer
	reviewOnce sync.Once
	// home is the user's home dir, for expanding ~/ in a kit agent's workdir.
	home string
}

// MCPStatus reports each declared server's health, nil when none is declared.
func (p *Parts) MCPStatus() []mcp.ServerStatus {
	if p.mcp == nil {
		return nil
	}
	return p.mcp.Status()
}

// Store is the runs store; nil only when the open failed, which is logged
// and non-fatal.
func (p *Parts) Store() *runs.Store { return p.st }

// LoadedConfig exposes the parsed config, hooks included, for callers that
// need the hook surface rather than the accessors below.
func (p *Parts) LoadedConfig() *config.LoadedConfig { return p.lc }

// Log returns the application logger (never nil once BuildParts succeeded).
func (p *Parts) Log() applog.Logger { return p.log }

// ConfigDir is the absolute config directory these parts came from.
func (p *Parts) ConfigDir() string { return p.configDir }

// BackgroundMaxConcurrent is the declared job cap; 0 = unset.
func (p *Parts) BackgroundMaxConcurrent() int { return p.lc.BackgroundMaxConcurrent }

func (p *Parts) ModelCount() int { return len(p.lc.Models) }

func (p *Parts) AgentCount() int {
	if p.kit == nil {
		return 0
	}
	return len(p.kit.Agents)
}

// Telegram returns the parsed telegram: block (zero value if absent).
func (p *Parts) Telegram() config.TelegramConfig { return p.lc.Telegram() }

// Cron returns the kit's `cron:` jobs, in declaration order.
func (p *Parts) Cron() []kit.CronJob {
	if p.kit == nil {
		return nil
	}
	return p.kit.Crons
}

// RunsKeepDays feeds the runs janitor at startup. Default 30, 0 = forever.
func (p *Parts) RunsKeepDays() int { return p.lc.RunsKeepDays }

// MediaKeepDays feeds the media janitor at startup. Default 0 = forever.
func (p *Parts) MediaKeepDays() int { return p.lc.MediaKeepDays }

// RunsRoot is the .shell3_project directory the Store was opened against, the
// root Sweep expects. Derived from runsDir rather than stored separately,
// since Store already keys off that relationship.
func (p *Parts) RunsRoot() string { return filepath.Dir(p.runsDir) }

// AgentRuntime assembles an agent's model client, profile and tool defs. ""
// uses the kit's first agent; an undeclared name is an error.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	return p.KitAgentRuntime(name)
}

// SubagentWorkdir is an employee's declared workdir resolved against the
// config directory (with ~/ expanded). "" inherits the spawner's, the default
// when none is declared. AgentContextBase uses the same rule for context:
// files, so an agent executes beside the memory it reads.
func (p *Parts) SubagentWorkdir(name string) string {
	if p.kit == nil {
		return ""
	}
	for _, a := range p.kit.Agents {
		if a.Name == name && a.Workdir != "" {
			return AgentContextBase(p.configDir, p.home, a.Workdir)
		}
	}
	return ""
}

// EnvironmentReminder renders the host-injected Environment standing
// reminder: the agent's own config path, the active model, this session's id,
// and where conversation history lives.
//
// A package-level function rather than a *Parts method, so internal/shell3 can
// render it from the chat.Config fields it already holds and the wording lives
// in one place. "" when runsDir is empty, so it never advertises a path the
// agent cannot use.
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
		// Name what is on disk: listing files the install lacks teaches the
		// model a layout it will contradict itself about.
		fmt.Fprintf(&b, "- config: `%s` (your config directory: %s (wiring, agents, tools, the gate), skills/, projects/<agent>/skills/ — edit it via the self-evolve skill)\n", configDir, kit.FileName)
	}
	// From paths.ProjectDirName, so renaming the project dir cannot leave the
	// reminder teaching paths that no longer exist.
	fmt.Fprintf(&b, "- history: every conversation (subagent runs included) is stored in `%s/shell3.db`; recall past sessions with the history tool when you have it (search, then read around a hit)\n", paths.ProjectDirName)
	fmt.Fprintf(&b, "- background job logs: `%s/runs/<session>/jobs/<job>.log` (plain files)\n", paths.ProjectDirName)
	b.WriteString("</system-reminder>")
	return b.String()
}

// RefreshPromptFor re-renders an agent's system prompt, so a context: file
// edited mid-conversation is current next turn. Names come pre-validated from
// Agent; an impossible miss returns "" and the turn keeps its prompt.
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
	// PromptSuffix appends per-session text, re-rendered every turn.
	PromptSuffix func() string
}

// bridgeVerdict maps a config gate verdict to the chat package's, field by
// field. The two Action enums are independent iota blocks, so an explicit
// mapping keeps this security boundary correct if either is reordered, and an
// unrecognized action fails closed.
func bridgeVerdict(v config.ToolCallVerdict) chat.ToolCallVerdict {
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

// SessionConfig derives a per-session chat.Config from the shared parts. Its
// closures consult only declared config plus the session's own agent.
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
	activeName := rt.Agent
	cfg := chat.Config{
		Store:          p.st,
		RunsDir:        p.runsDir,
		WorkDir:        workdir,
		ConfigDir:      p.ConfigDir(),
		ConfigWarnings: append(append([]string{}, p.lc.Warnings()...), p.mcpWarns...),
		Log:            p.log,
		Headless:       so.Headless,
		RefreshPrompt:  func() string { return p.RefreshPromptFor(activeName) },
		PromptSuffix:   so.PromptSuffix,
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
	// The kit's gate:, run before every tool. Each agent has its own or none.
	if p.lc.HasToolCall() {
		cfg.RunToolCall = func(ctx context.Context, name, command, argsJSON string, headless bool) chat.ToolCallVerdict {
			return bridgeVerdict(p.lc.RunToolCall(ctx, activeName, name, command, argsJSON, headless))
		}
		// Keyed by the active agent, so one runaway agent's denial-breaker
		// tally never hard-stops another. A nil reviewer leaves
		// cfg.ReviewToolCall nil and reviews fail closed downstream.
		if rev := p.Reviewer(); rev != nil {
			cfg.ReviewToolCall = func(ctx context.Context, req chat.ToolReviewRequest) (bool, string) {
				return rev.Review(ctx, activeName, review.Request{
					Name:               req.Name,
					Command:            req.Command,
					Reason:             req.Reason,
					WorkDir:            req.WorkDir,
					Headless:           req.Headless,
					TrustedUserContext: req.TrustedUserContext,
					Messages:           req.Messages,
				})
			}
		}
	}
	// The kit's `note:` function: the per-agent output rewrite.
	if p.lc.HasToolResult() {
		cfg.RunToolResult = func(ctx context.Context, name, argsJSON, output string) string {
			return p.lc.RunToolResult(ctx, activeName, name, argsJSON, output)
		}
	}
	// The subscription check comes FIRST, before rendering to JSON:
	// assistant_token fires per streamed token, so an unsubscribed kind must
	// cost a map lookup and nothing else. Delivery then hands off to the
	// shared dispatcher — a turn never waits on an observer.
	if p.lc.HasEvent() {
		if d := p.eventDispatcher(); d != nil {
			cfg.OnEvent = func(ev chat.Event) {
				kind := ev.Kind.String()
				if !p.lc.SubscribesTo(activeName, kind) {
					return
				}
				d.Post(activeName, kind, eventPayload(activeName, ev))
			}
		}
	}
	cfg.ApplyActiveAgent(rt)
	return cfg, nil
}

// eventDispatcher is the shared delivery worker, started on first use. One
// per Parts, so subscribers run serially install-wide and a shell function
// appending to a log never races itself.
func (p *Parts) eventDispatcher() *eventDispatcher {
	p.eventOnce.Do(func() {
		p.events = newEventDispatcher(eventQueueDepth, p.lc.RunEvent, p.log)
	})
	return p.events
}

// CloseEvents stops the dispatcher, safe when none was started.
func (p *Parts) CloseEvents() {
	p.eventOnce.Do(func() {}) // claim the once so a late caller cannot start one
	if p.events != nil {
		p.events.Close()
	}
}

// BuildParts assembles the shared runtime parts. Callers MUST invoke the
// returned cleanup once; it closes the MCP connections, store and log.
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
	// ResolveConfigDir already refused a directory with no kit.
	if err := p.LoadKit(filepath.Join(b.configDir, kit.FileName)); err != nil {
		b.closeAll()
		return nil, noop, err
	}
	// The dispatcher starts lazily, so its closer registers here: a reload
	// must stop the worker whether or not one was ever spun up.
	b.closers = append(b.closers, p.CloseEvents)
	return p, b.closeAll, nil
}

// builder accumulates state and open resources across BuildParts' stages.
// closers is a LIFO stack: each stage pushes as it acquires, and closeAll
// unwinds in reverse (store → lc → log).
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

// resolvePaths resolves the config path, builds the path sets, and ensures the
// global root and project dirs exist. A project's identity is its directory.
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

// openLog opens the rotating app log. A failure warns on stderr — the log
// being unavailable to record it — and falls back to Noop.
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

// loadConfig loads the config directory. Hooks and .env resolve against it
// while the agent's bash cwd stays opts.CWD — different on purpose.
func (b *builder) loadConfig() error {
	lc, err := config.Load(b.configDir)
	if err != nil {
		return err
	}
	b.lc = lc
	// To both the log, for a durable record, and stderr, which reaches
	// headless runs directly.
	for _, w := range lc.Warnings() {
		b.log.Warn("config warning", "detail", w)
		fmt.Fprintln(os.Stderr, "shell3: config warning: "+w)
	}
	return nil
}

// connectMCP connects the server manager when an mcp: block is declared.
// Synchronous by design — servers dial in parallel under their own timeouts,
// which makes tool defs plain static config for a few seconds at startup. A
// down server is a warning; its tools are absent until the next reload.
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

// openStore opens the per-project database unconditionally: it persists the
// conversation and backs the history tool. A failure warns and proceeds with
// a nil store. The handle rides the closer stack, so a parked generation
// releases it.
func (b *builder) openStore() {
	if s, e := runs.Open(b.l.Root); e == nil {
		b.st = s
		b.closers = append(b.closers, func() { _ = s.Close() })
	} else {
		b.log.Warn("open store failed — history unavailable", "error", e)
		fmt.Fprintln(os.Stderr, "shell3: warning: open store failed — conversations will not persist and history is unavailable: "+e.Error())
	}
}

// reviewMaxTokens caps the reviewer's compact JSON assessment. A reasoning
// model spends thinking tokens first, and a tiny cap would truncate the final
// object and fail every review closed.
const reviewMaxTokens = 1024

// Reviewer is the shared reviewer behind the gate's {review} verdict, built on
// first use: review_model, defaulting to the main agent's, on a DEDICATED
// client at temperature 0 — never the agent's own, whose params a reviewer
// override would corrupt. nil when the model cannot resolve, and the chat
// layer then fails review verdicts closed with a named reason.
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
		zero := 0.0
		p.reviewer = review.New(newModelClient(md, llm.RequestParams{
			Temperature:     &zero,
			MaxTokens:       reviewMaxTokens,
			ReasoningEffort: md.Reasoning,
		}), p.lc.ReviewPolicy)
	})
	return p.reviewer
}

// buildClient constructs a streaming client plus its request params from a
// configured model.
func buildClient(md config.Model) (chat.LLMClient, llm.RequestParams) {
	rp := llm.RequestParams{
		ReasoningEffort: md.Reasoning,
		MaxTokens:       md.MaxTokens,
		Temperature:     md.Temperature,
	}
	return newModelClient(md, rp), rp
}

// newModelClient dials a configured model with the given request params. The
// one place a model's endpoint, key, and `extra:` fields reach the adapter —
// the reviewer takes the same route with its own params.
func newModelClient(md config.Model, rp llm.RequestParams) *openai.Client {
	cl := openai.NewClient(md.BaseURL, md.APIKey, md.ModelID)
	cl.SetParams(rp)
	if md.Extra != nil {
		cl.SetExtra(md.Extra)
	}
	return cl
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
