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
	"time"

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

	m            luacfg.Model
	client       chat.LLMClient
	rp           llm.RequestParams
	models       []chat.ModelInfo
	coreMemories []store.MemoryEntry

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
	if err := b.resolveModel(); err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	b.openStore() // non-fatal; may push the store closer
	return b.assemble(), b.closeAll, nil
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

// resolveModel resolves the agent's configured model, builds the initial client
// and request params, and enumerates every model for the /model command.
func (b *builder) resolveModel() error {
	m, ok := b.lc.Model(b.lc.Agent.ModelName)
	if !ok {
		return fmt.Errorf("agent references unknown model %q", b.lc.Agent.ModelName)
	}
	b.m = m
	b.client, b.rp = buildClient(m)
	for _, md := range b.lc.Models {
		b.models = append(b.models, chat.ModelInfo{
			Name:          md.Name,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		})
	}
	return nil
}

// openStore opens the SQLite store when the agent gates memory or history, and
// loads core memories. Both are non-fatal: a failure warns and proceeds.
func (b *builder) openStore() {
	if b.lc.Agent.Gates.Memory || b.lc.Agent.Gates.History {
		if s, e := store.Open(b.proj.DB); e == nil {
			b.st = s
			b.closers = append(b.closers, func() { _ = s.Close() })
		} else {
			b.log.Warn("open store failed — memory and history unavailable", "error", e)
		}
	}
	if b.st != nil {
		if mems, e := b.st.MemoryQuery(true, 0); e != nil {
			b.log.Warn("load core memories failed", "error", e)
		} else {
			b.coreMemories = mems
		}
	}
}

// assemble renders the persona and builds the final chat.Config, including the
// switchModel / buildPrompt / ToolGuard closures stored into it.
func (b *builder) assemble() chat.Config {
	// buildPrompt renders the system prompt with a fresh timestamp each call.
	// Used once now for the initial prompt and again by /clear (via
	// cfg.RefreshPrompt) so a new conversation re-stamps the clock.
	buildPrompt := func() string {
		return b.lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          b.opts.CWD,
			Model:        b.m.ModelID,
			CoreMemories: b.coreMemories,
		})
	}
	// switchModel rebuilds the active client when the user switches by name.
	switchModel := func(name string) (chat.ActiveModel, error) {
		md, ok := b.lc.Model(name)
		if !ok {
			return chat.ActiveModel{}, fmt.Errorf("unknown model %q", name)
		}
		cl, p := buildClient(md)
		return chat.ActiveModel{
			Client:        cl,
			Params:        p,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		}, nil
	}

	customDefs := b.lc.CustomToolsFor(b.lc.Agent.CustomTools)
	hasSkills := b.lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(b.lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         b.lc.Agent.Name,
		SystemPrompt: buildPrompt(),
		Tools:        toolDefs,
		Parameters:   b.rp,
	}

	customNames := make(map[string]bool, len(b.lc.Agent.CustomTools))
	for _, n := range b.lc.Agent.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	return chat.Config{
		LLM:             b.client,
		Store:           b.st,
		Personality:     pers,
		RefreshPrompt:   buildPrompt,
		WorkDir:         b.opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", b.lc.Agent.Name, b.m.ModelID),
		ModeLabel:       b.lc.Agent.Name,
		ProjectRef:      b.uuid,
		ActiveSkills:    b.lc.Agent.Skills,
		ActiveTools:     toolNames,
		ContextWindow:   b.m.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      b.lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := b.lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Params:      b.rp,
		Log:         b.log,
		OutPath:     b.opts.OutPath,
		Headless:    b.opts.Headless,
		Models:      b.models,
		SwitchModel: switchModel,
	}
}

// buildClient constructs a streaming client plus its request params from a
// configured model. Reused for the initial client and for /model switches.
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
