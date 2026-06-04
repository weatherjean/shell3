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
	"github.com/weatherjean/shell3/internal/docs"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/luacfg"
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

	log applog.Logger
	lc  *luacfg.LoadedConfig
	st  *store.Store

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
	b.openStore() // non-fatal; may push the store closer
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

	return chat.Config{
		LLM:             rt.LLM,
		Store:           b.st,
		Personality:     rt.Personality,
		RefreshPrompt:   buildPrompt,
		WorkDir:         b.opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID),
		ModeLabel:       rt.ModeLabel,
		ProjectRef:      b.uuid,
		ActiveSkills:    rt.ActiveSkills,
		ActiveTools:     rt.ActiveTools,
		ContextWindow:   rt.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      b.lc.CallTool,
		CustomToolNames: rt.CustomToolNames,
		ToolGuard:       rt.ToolGuard,
		Params:          rt.Params,
		Log:             b.log,
		OutPath:         b.opts.OutPath,
		Headless:        b.opts.Headless,
		AgentNames:      agentNames,
		SwitchAgent:     switchAgent,
	}, nil
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
