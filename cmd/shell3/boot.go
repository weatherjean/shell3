package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// openAppLog opens the rotating application log, falling back to a Noop logger
// (and a no-op closer) when the file cannot be opened. Shared by run + web.
func openAppLog(logFile string) (applog.Logger, io.Closer) {
	const logMaxBytes = 2 * 1024 * 1024 // 2 MB per log file
	const logArchives = 3               // keep .1 .2 .3 → max ~8 MB total
	log, closer, err := applog.Open(logFile, logMaxBytes, logArchives)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		return applog.Noop{}, io.NopCloser(nil)
	}
	return log, closer
}

// buildChatConfig bootstraps directories, loads shell3.lua, constructs the
// OpenAI-compatible client, optional store, persona, and returns a ready
// chat.Config plus a cleanup closure (closes the Lua state and store). It is
// the shared core of the interactive (run) and web entry points.
func buildChatConfig(configPath, cwd, homeDir, outPath string, headless bool, log applog.Logger) (chat.Config, func(), error) {
	g := paths.NewGlobal(homeDir)
	l := paths.NewLocal(cwd)

	if err := bootstrap.EnsureGlobal(g); err != nil {
		return chat.Config{}, func() {}, err
	}
	uuid, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		return chat.Config{}, func() {}, err
	}
	proj := paths.NewProject(g, uuid)

	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		return chat.Config{}, func() {}, err
	}
	cleanup := func() { lc.Close() }

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

	m, _ := lc.Model(lc.Agent.ModelName) // Load already validated existence.
	client, rp := buildClient(m)

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
		return chat.ActiveModel{Client: cl, Params: p, ModelID: md.ModelID, ContextWindow: md.ContextWindow}, nil
	}

	var st *store.Store
	if lc.Agent.Gates.Memory || lc.Agent.Gates.History {
		if s, err := store.Open(proj.DB); err == nil {
			st = s
			prev := cleanup
			cleanup = func() { _ = st.Close(); prev() }
		} else {
			log.Warn("open store failed — memory and history unavailable", "error", err)
		}
	}

	var coreMemories []store.MemoryEntry
	if st != nil {
		mems, err := st.MemoryQuery(true, 0)
		if err != nil {
			log.Warn("load core memories failed", "error", err)
		} else {
			coreMemories = mems
		}
	}

	sysPrompt := lc.BuildPersona(luacfg.RuntimeData{
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          cwd,
		Model:        m.ModelID,
		CoreMemories: coreMemories,
	})

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         lc.Agent.Name,
		SystemPrompt: sysPrompt,
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
		WorkDir:         cwd,
		StatusLine:      fmt.Sprintf("%s │ %s", lc.Agent.Name, m.ModelID),
		ModeLabel:       lc.Agent.Name,
		ProjectRef:      uuid,
		ActiveSkills:    lc.Agent.Skills,
		ActiveTools:     toolNames,
		ContextWindow:   m.ContextWindow,
		Docs:            docsContent,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Params:      rp,
		Log:         log,
		OutPath:     outPath,
		Headless:    headless,
		Models:      models,
		SwitchModel: switchModel,
	}
	return cfg, cleanup, nil
}
