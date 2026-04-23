package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/agent"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/history"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/memory"
	"github.com/weatherjean/shell3/internal/output"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/tools"
)

type runFlags struct {
	personality string
	configPath  string
	model       string
	baseURL     string
	apiKey      string
	memoryDB    string
	historyMD   string
	stream      bool
	out         string
	skillPaths  []string
	noBash      bool
	noMemory    bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	bindRunFlags(cmd, f)
	return cmd
}

func bindRunFlags(cmd *cobra.Command, f *runFlags) {
	cmd.Flags().StringVar(&f.personality, "personality", "", "Named personality")
	cmd.Flags().StringVar(&f.configPath, "config", "", "Personality YAML file path")
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().StringVar(&f.memoryDB, "memory-db", "", "SQLite memory DB path")
	cmd.Flags().StringVar(&f.historyMD, "history-md", "", "Markdown history file path")
	cmd.Flags().BoolVar(&f.stream, "stream", false, "Emit JSONL event stream")
	cmd.Flags().StringVar(&f.out, "out", "", "Pipe output to this command")
	cmd.Flags().StringSliceVar(&f.skillPaths, "skills", nil, "Additional skill directories")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory tools")
}

func runAgent(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()

	projCfg, err := config.LoadProject(cwd)
	if err != nil {
		return err
	}
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}
	if err := config.Validate(projCfg, creds); err != nil {
		return err
	}

	model, baseURL, apiKey := resolveConnectionParams(projCfg, creds, f)
	memoryDB := coalesce(f.memoryDB, projCfg.MemoryDB)
	historyMD := coalesce(f.historyMD, projCfg.HistoryMD)

	emitter := buildEmitter(f.stream)
	ts, memDB, err := buildTools(cwd, f, memoryDB)
	if err != nil {
		return err
	}
	if memDB != nil {
		defer memDB.Close()
	}

	systemPrompt := buildSystemPrompt(f.skillPaths)
	sess, err := buildSession(historyMD)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(hooks.Config(projCfg.Hooks))
	agentCfg := agent.Config{
		SystemPrompt: systemPrompt,
		LLM:          llm.NewClient(baseURL, apiKey, model),
		Tools:        ts,
		Hooks:        hookRunner,
		Emitter:      emitter,
	}

	hookRunner.OnSessionStart(ctx)
	defer hookRunner.OnSessionEnd(ctx)

	if initialInput != "" {
		return runAndSave(ctx, agentCfg, sess, initialInput, historyMD)
	}
	return runInteractive(ctx, agentCfg, sess, historyMD)
}

func resolveConnectionParams(cfg *config.ProjectConfig, creds *config.Credentials, f *runFlags) (model, baseURL, apiKey string) {
	model = cfg.Model
	if f.model != "" {
		model = f.model
	}
	provCreds, _ := creds.Get(cfg.Provider)
	baseURL = provCreds.BaseURL
	if f.baseURL != "" {
		baseURL = f.baseURL
	}
	apiKey = provCreds.APIKey
	if f.apiKey != "" {
		apiKey = f.apiKey
	}
	return
}

func buildEmitter(stream bool) output.Emitter {
	if stream {
		return output.NewJSONLEmitter(os.Stdout)
	}
	return output.NewPlainEmitter(os.Stdout)
}

func buildTools(cwd string, f *runFlags, memoryDB string) ([]tools.Tool, *memory.DB, error) {
	var ts []tools.Tool
	if !f.noBash {
		ts = append(ts, tools.NewBashTool(cwd, 30))
	}
	if f.noMemory || memoryDB == "" {
		return ts, nil, nil
	}
	db, err := memory.Open(memoryDB)
	if err != nil {
		return nil, nil, fmt.Errorf("run: open memory db: %w", err)
	}
	ts = append(ts, tools.NewMemorySearchTool(db), tools.NewMemoryStoreTool(db))
	return ts, db, nil
}

func buildSystemPrompt(extraSkillPaths []string) string {
	dirs := append([]string{".shell3/skills"}, extraSkillPaths...)
	loadedSkills, _ := skills.LoadAll(dirs)
	return "You are an expert software engineer. Use tools to accomplish tasks.\n" +
		skills.BuildSection(loadedSkills)
}

func buildSession(historyMD string) (*agent.Session, error) {
	sess := &agent.Session{}
	if historyMD == "" {
		return sess, nil
	}
	msgs, err := history.Load(historyMD)
	if err != nil {
		return nil, err
	}
	sess.Messages = msgs
	return sess, nil
}

func runAndSave(ctx context.Context, cfg agent.Config, sess *agent.Session, input, historyMD string) error {
	if err := agent.RunTurn(ctx, cfg, sess, input); err != nil {
		return err
	}
	if historyMD != "" {
		return history.Save(historyMD, sess.Messages)
	}
	return nil
}

func runInteractive(ctx context.Context, cfg agent.Config, sess *agent.Session, historyMD string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		if err := runAndSave(ctx, cfg, sess, line, historyMD); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		fmt.Print("\n> ")
	}
	return nil
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
