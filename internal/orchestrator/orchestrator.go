// Package orchestrator assembles the attached, Lisp-configured shell3 turn.
package orchestrator

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
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
	scheduler "github.com/weatherjean/shell3/internal/schedule"
	"github.com/weatherjean/shell3/internal/shell3"
)

type clientFactory func(lispconfig.Model, string) llm.Streamer

func defaultClient(m lispconfig.Model, key string) llm.Streamer {
	client := openai.NewClient(m.BaseURL, key, m.ID)
	client.SetParams(llm.RequestParams{ReasoningEffort: m.Reasoning, MaxTokens: m.MaxTokens})
	return client
}

// Open builds a native runtime whose only model-facing tools are bash and
// bash_bg. The Lisp file is parsed before any secret is read.
func Open(ctx context.Context, configPath, workDir string) (*shell3.Runtime, error) {
	return openWithClient(ctx, configPath, workDir, defaultClient)
}

// OpenTelegram builds the same orchestrator runtime with a transport reminder
// for the single Telegram file-delivery tool installed by the adapter.
func OpenTelegram(ctx context.Context, configPath, workDir string) (*shell3.Runtime, error) {
	return openWithClientMode(ctx, configPath, workDir, true, defaultClient)
}

// Reload validates and resolves a complete kit generation before atomically
// replacing the configuration of every idle session.
func Reload(rt *shell3.Runtime, configPath, workDir string, telegram bool) (*lispconfig.Config, error) {
	var err error
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		return nil, err
	}
	if _, err := scheduler.Resolve(configPath, cfg); err != nil {
		return nil, err
	}
	factory, err := sessionFactory(cfg, configPath, workDir, telegram, rt.Store(), rt.Logger(), defaultClient)
	if err != nil {
		return nil, err
	}
	if err := rt.ReloadConfig(factory); err != nil {
		return nil, err
	}
	return cfg, nil
}

func openWithClient(ctx context.Context, configPath, workDir string, makeClient clientFactory) (*shell3.Runtime, error) {
	return openWithClientMode(ctx, configPath, workDir, false, makeClient)
}

func openWithClientMode(ctx context.Context, configPath, workDir string, telegram bool, makeClient clientFactory) (*shell3.Runtime, error) {
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		return nil, err
	}
	if _, err := scheduler.Resolve(configPath, cfg); err != nil {
		return nil, err
	}
	if cfg.Main == nil {
		return nil, fmt.Errorf("%s: missing orchestrator form", configPath)
	}
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}
	local := paths.NewLocal(workDir)
	if err := bootstrap.EnsureProject(local); err != nil {
		return nil, err
	}
	log, logCloser, err := applog.Open(local.Errors)
	if err != nil {
		return nil, err
	}
	store, err := runs.Open(local.Root)
	if err != nil {
		_ = logCloser.Close()
		return nil, err
	}
	factory, err := sessionFactory(cfg, configPath, workDir, telegram, store, log, makeClient)
	if err != nil {
		_ = store.Close()
		_ = logCloser.Close()
		return nil, err
	}
	rt, err := shell3.NewConfiguredRuntime(ctx, workDir, store, 8, func() {
		_ = store.Close()
		_ = logCloser.Close()
	}, factory)
	if err != nil {
		_ = store.Close()
		_ = logCloser.Close()
		return nil, err
	}
	rt.SetLogger(log)
	log.Info("runtime opened", "workdir", workDir, "config", configPath, "telegram", telegram)
	return rt, nil
}

func sessionFactory(cfg *lispconfig.Config, configPath, workDir string, telegram bool, store *runs.Store, log applog.Logger, makeClient clientFactory) (func(shell3.SessionOpts) (chat.Config, error), error) {
	if cfg.Main == nil {
		return nil, fmt.Errorf("%s: missing orchestrator form", configPath)
	}
	model := cfg.Models[cfg.Main.Model]
	apiKey, err := lispconfig.ResolveSecret(model.APIKeyEnv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve shell3 executable: %w", err)
	}
	local := paths.NewLocal(workDir)
	prompt := renderPrompt(cfg, configPath, workDir, executable, telegram)
	defs := coreToolDefinitions()
	factory := func(opts shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM:          makeClient(model, apiKey),
			Store:        store,
			WorkDir:      workDir,
			PromptSuffix: opts.PromptSuffix,
			RenderEnvironment: func(sessionID string) string {
				return fmt.Sprintf("<system-reminder>\nEnvironment:\n- shell3 config: %s\n- embedded skills: %d (use shell3 config skill to read one)\n- runs: %s\n- model: %s\n- session: %s\n</system-reminder>",
					configPath, len(cfg.Skills), local.Runs, model.ID, sessionID)
			},
			Profile: chat.AgentProfile{
				SystemPrompt: prompt,
				Tools:        defs,
			},
			ModelID:    model.ID,
			AgentKnobs: contextPolicy(model.ContextWindow),
			Log:        log,
			Headless:   opts.Headless,
		}, nil
	}
	return factory, nil
}

func contextPolicy(window int) chat.AgentKnobs {
	if window <= 0 {
		return chat.AgentKnobs{}
	}
	compactAt := window * 80 / 100
	return chat.AgentKnobs{
		ContextWindow: window,
		CompactAt:     compactAt,
		PruneAt:       compactAt * 60 / 100,
	}
}

func renderPrompt(cfg *lispconfig.Config, configPath, workDir, executable string, telegram bool) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(cfg.Main.Prompt))
	if telegram {
		b.WriteString("\n\nThis session is attached through Telegram and has one additional transport tool named `telegram`. It sends a local file to the current chat. Ordinary text is delivered by your normal assistant reply; use the tool only when the user needs a file.")
	}
	if strings.TrimSpace(cfg.Memory) != "" {
		b.WriteString("\n\n## Memory\n\n")
		b.WriteString(strings.TrimSpace(cfg.Memory))
	}
	b.WriteString(renderSkills(cfg.Skills, executable, configPath))
	b.WriteString("\n\n## Runtime\n\n")
	fmt.Fprintf(&b, "- shell3 executable: %s\n- shell3 config: %s\n- work directory: %s", executable, configPath, workDir)
	return b.String()
}

func renderSkills(skills []lispconfig.Skill, executable, configPath string) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Skills\n\nRead a skill only when its description applies, using:\n`" + shellQuote(executable) + " config skill " + shellQuote(configPath) + " SKILL_NAME`\n\n")
	for _, skill := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", skill.Name, strings.TrimSpace(skill.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
