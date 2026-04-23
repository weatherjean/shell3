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
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/output"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tools"
)

type runFlags struct {
	model      string
	baseURL    string
	apiKey     string
	storeDB    string
	stream     bool
	out        string
	skillPaths []string
	noBash     bool
	noMemory   bool
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
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().StringVar(&f.storeDB, "store-db", "", "SQLite store DB path")
	cmd.Flags().BoolVar(&f.stream, "stream", false, "Emit JSONL event stream")
	cmd.Flags().StringVar(&f.out, "out", "", "Pipe output to this command")
	cmd.Flags().StringSliceVar(&f.skillPaths, "skills", nil, "Additional skill directories")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
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
	storeDB := coalesce(f.storeDB, projCfg.StoreDB, ".shell3/shell3.db")

	emitter := buildEmitter(f.stream)
	st, ts, err := buildTools(cwd, f, storeDB)
	if err != nil {
		return err
	}
	if st != nil {
		defer st.Close()
	}

	systemPrompt := buildSystemPrompt(f.skillPaths)
	sess := &agent.Session{}

	var sessionID int64
	if st != nil {
		sessionID, err = st.StartSession()
		if err != nil {
			return fmt.Errorf("run: start session: %w", err)
		}
		defer st.EndSession(sessionID)
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
		return runAndSave(ctx, agentCfg, sess, initialInput, st, sessionID)
	}
	return runInteractive(ctx, agentCfg, sess, st, sessionID)
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

func buildTools(cwd string, f *runFlags, storeDB string) (*store.Store, []tools.Tool, error) {
	var ts []tools.Tool
	if !f.noBash {
		ts = append(ts, tools.NewBashTool(cwd, 30))
	}
	if f.noMemory || storeDB == "" {
		return nil, ts, nil
	}
	st, err := store.Open(storeDB)
	if err != nil {
		return nil, nil, fmt.Errorf("run: open store: %w", err)
	}
	ts = append(ts,
		tools.NewMemoryStoreTool(st),
		tools.NewMemorySearchTool(st),
		tools.NewMemoryRemoveTool(st),
		tools.NewHistorySearchTool(st),
	)
	return st, ts, nil
}

func buildSystemPrompt(extraSkillPaths []string) string {
	dirs := append([]string{".shell3/skills"}, extraSkillPaths...)
	loadedSkills, _ := skills.LoadAll(dirs)
	return "You are an expert software engineer. Use tools to accomplish tasks.\n" +
		skills.BuildSection(loadedSkills)
}

func runAndSave(ctx context.Context, cfg agent.Config, sess *agent.Session, input string, st *store.Store, sessionID int64) error {
	prevLen := len(sess.Messages)
	if err := agent.RunTurn(ctx, cfg, sess, input); err != nil {
		return err
	}
	if st != nil {
		for _, m := range sess.Messages[prevLen:] {
			if m.Role == llm.RoleUser || m.Role == llm.RoleAssistant {
				st.AppendHistory(sessionID, string(m.Role), m.Content)
			}
		}
	}
	return nil
}

func runInteractive(ctx context.Context, cfg agent.Config, sess *agent.Session, st *store.Store, sessionID int64) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		if err := runAndSave(ctx, cfg, sess, line, st, sessionID); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		fmt.Print("\n> ")
	}
	return nil
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
