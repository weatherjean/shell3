package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/docs"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

type runFlags struct {
	configPath string
	outPath    string
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.TrimSpace(strings.Join(args, " "))
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runChat(cmd.Context(), f, input)
		},
	}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>. Enables headless mode.")
	return cmd
}

// resolveConfigPath returns the shell3.lua to load: the explicit flag, else
// ./shell3.lua if it exists, else ~/.shell3/shell3.lua if it exists. Returns
// an error when nothing is found.
func resolveConfigPath(flag, cwd, homeDir string) (string, error) {
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

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	configPath, err := resolveConfigPath(f.configPath, cwd, homeDir)
	if err != nil {
		return err
	}

	headless := f.outPath != "" || (!term.IsTerminal(int(os.Stdin.Fd())) && initialInput != "")
	if headless {
		_ = os.Setenv("SHELL3_HEADLESS", "1")
		if f.outPath != "" {
			_ = os.Setenv("SHELL3_OUT", f.outPath)
		}
	}

	g := paths.NewGlobal(homeDir)
	l := paths.NewLocal(cwd)

	if err := bootstrap.EnsureGlobal(g); err != nil {
		return err
	}
	uuid, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		return err
	}

	const logMaxBytes = 2 * 1024 * 1024 // 2 MB per log file
	const logArchives = 3               // keep .1 .2 .3 → max ~8 MB total
	log, logCloser, err := applog.Open(g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		// Non-fatal: fall back to Noop so the rest of startup continues.
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		log = applog.Noop{}
		logCloser = io.NopCloser(nil)
	}
	defer logCloser.Close()
	proj := paths.NewProject(g, uuid)

	// The Lua/.env workdir is the config file's directory; the agent's bash
	// cwd stays os.Getwd(). These differ on purpose.
	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		return err
	}
	defer lc.Close()

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

	m, _ := lc.Model(lc.Agent.ModelName) // Load already validated existence.
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
		if s, err := store.Open(proj.DB); err == nil {
			st = s
			defer func() { _ = st.Close() }()
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

	// buildPrompt renders the system prompt with a fresh timestamp each call.
	// Used once now for the initial prompt and again by /clear (via
	// cfg.RefreshPrompt) so a new conversation re-stamps the clock.
	buildPrompt := func() string {
		return lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          cwd,
			Model:        m.ModelID,
			CoreMemories: coreMemories,
		})
	}
	sysPrompt := buildPrompt()

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
		RefreshPrompt:   buildPrompt,
		WorkDir:         cwd,
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
		OutPath:     f.outPath,
		Headless:    headless,
		Models:      models,
		SwitchModel: switchModel,
	}

	if initialInput != "" {
		return tui.RunOnce(ctx, cfg, initialInput)
	}
	return tui.RunInteractive(ctx, cfg)
}
