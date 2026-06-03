// Package agentsetup is the shared config assembly used by every shell3
// front-end (the bubbletea TUI, the stdout one-shot, and the pkg/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads shell3.lua, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/docs"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
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

// Build assembles the full chat.Config. The returned cleanup closes the store,
// the Lua state, and the log; callers MUST invoke it.
func Build(opts Options) (chat.Config, func(), error) {
	noop := func() {}

	configPath, err := ResolveConfigPath(opts.ConfigPath, opts.CWD, opts.HomeDir)
	if err != nil {
		return chat.Config{}, noop, err
	}

	g := paths.NewGlobal(opts.HomeDir)
	l := paths.NewLocal(opts.CWD)

	if err := bootstrap.EnsureGlobal(g); err != nil {
		return chat.Config{}, noop, err
	}
	uuid, err := bootstrap.EnsureProject(l, g, opts.CWD)
	if err != nil {
		return chat.Config{}, noop, err
	}

	const logMaxBytes = 2 * 1024 * 1024
	const logArchives = 3
	log, logCloser, err := applog.Open(g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		// Non-fatal: fall back to Noop so startup continues. Warn on stderr
		// since the log itself is unavailable to record this.
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		log = applog.Noop{}
		logCloser = io.NopCloser(nil)
	}
	proj := paths.NewProject(g, uuid)

	// The Lua/.env workdir is the config file's directory; the agent's bash
	// cwd stays opts.CWD. These differ on purpose.
	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		_ = logCloser.Close()
		return chat.Config{}, noop, err
	}

	// buildClient constructs a streaming client plus its request params from a
	// configured model. Reused for the initial client and for /model switches.
	buildClient := func(md luacfg.Model) (chat.LLMClient, llm.RequestParams) {
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

	m, ok := lc.Model(lc.Agent.ModelName)
	if !ok {
		lc.Close()
		_ = logCloser.Close()
		return chat.Config{}, noop, fmt.Errorf("agent references unknown model %q", lc.Agent.ModelName)
	}
	client, rp := buildClient(m)

	// models enumerates every configured model for the /model command;
	// switchModel rebuilds the active client when the user switches by name.
	var models []chat.ModelInfo
	for _, md := range lc.Models {
		models = append(models, chat.ModelInfo{
			Name:          md.Name,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		})
	}
	switchModel := func(name string) (chat.ActiveModel, error) {
		md, ok := lc.Model(name)
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

	var st *store.Store
	if lc.Agent.Gates.Memory || lc.Agent.Gates.History {
		if s, e := store.Open(proj.DB); e == nil {
			st = s
		} else {
			log.Warn("open store failed — memory and history unavailable", "error", e)
		}
	}

	var coreMemories []store.MemoryEntry
	if st != nil {
		if mems, e := st.MemoryQuery(true, 0); e != nil {
			log.Warn("load core memories failed", "error", e)
		} else {
			coreMemories = mems
		}
	}

	// buildPrompt renders the system prompt with a fresh timestamp each call.
	// Used once now for the initial prompt and again by /clear (via
	// cfg.RefreshPrompt) so a new conversation re-stamps the clock.
	buildPrompt := func() string {
		return lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          opts.CWD,
			Model:        m.ModelID,
			CoreMemories: coreMemories,
		})
	}

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         lc.Agent.Name,
		SystemPrompt: buildPrompt(),
		Tools:        toolDefs,
		Parameters:   rp,
	}

	customNames := make(map[string]bool, len(lc.Agent.CustomTools))
	for _, n := range lc.Agent.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	cfg := chat.Config{
		LLM:             client,
		Store:           st,
		Personality:     pers,
		RefreshPrompt:   buildPrompt,
		WorkDir:         opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", lc.Agent.Name, m.ModelID),
		ModeLabel:       lc.Agent.Name,
		ProjectRef:      uuid,
		ActiveSkills:    lc.Agent.Skills,
		ActiveTools:     toolNames,
		ContextWindow:   m.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Params:      rp,
		Log:         log,
		OutPath:     opts.OutPath,
		Headless:    opts.Headless,
		Models:      models,
		SwitchModel: switchModel,
	}

	cleanup := func() {
		if st != nil {
			_ = st.Close()
		}
		lc.Close()
		_ = logCloser.Close()
	}
	return cfg, cleanup, nil
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
