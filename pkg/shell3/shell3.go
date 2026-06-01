package shell3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// Options configures a slim bootstrap for library embedding.
// All fields are optional except where noted.
type Options struct {
	// Ctx is reserved for future use. Defaults to context.Background() when nil.
	Ctx context.Context

	// HomeDir is the user home directory. Defaults to os.UserHomeDir() when empty.
	HomeDir string

	// ConfigPath is the path to shell3.lua. Defaults to
	// ~/.shell3/shell3.lua when empty.
	ConfigPath string

	// WorkDir is the working directory passed to tool handlers. Defaults to
	// os.Getwd when empty.
	WorkDir string

	// Headless when true marks the run as subprocess/embed.
	Headless bool
}

// New returns a chat.Config built from a shell3.lua config, suitable for
// embedding. It loads the config, constructs the OpenAI-compatible adapter for
// the agent's model, and assembles the persona carrier, custom-tool dispatch,
// and guard chain.
//
// The returned cleanup closes the Lua state; callers MUST invoke it.
func New(opts Options) (chat.Config, func(), error) {
	noop := func() {}

	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get home directory: %w", err)
		}
		homeDir = h
	}

	workDir := opts.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}

	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(homeDir, ".shell3", "shell3.lua")
	}

	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		return chat.Config{}, noop, fmt.Errorf("load config: %w", err)
	}
	cleanup := func() { lc.Close() }

	m, ok := lc.Model(lc.Agent.ModelName)
	if !ok {
		cleanup()
		return chat.Config{}, noop, fmt.Errorf("agent references unknown model %q", lc.Agent.ModelName)
	}

	client := openai.NewClient(m.BaseURL, m.APIKey, m.ModelID)
	rp := llm.RequestParams{
		ReasoningEffort: m.Reasoning,
		MaxTokens:       m.MaxTokens,
		Temperature:     m.Temperature,
	}
	client.SetParams(rp)
	if m.Extra != nil {
		client.SetExtra(m.Extra)
	}

	sysPrompt := lc.BuildPersona(luacfg.RuntimeData{CWD: workDir, Model: m.ModelID})

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := len(lc.Agent.Skills) > 0
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

	cfg := chat.Config{
		LLM:             client,
		Personality:     pers,
		WorkDir:         workDir,
		StatusLine:      lc.Agent.Name + " │ " + m.ModelID,
		ModeLabel:       lc.Agent.Name,
		ContextWindow:   m.ContextWindow,
		ActiveSkills:    lc.Agent.Skills,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Headless: opts.Headless,
		Log:      applog.Noop{},
		Params:   rp,
	}

	return cfg, cleanup, nil
}
