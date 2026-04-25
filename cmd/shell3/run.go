package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
)

type runFlags struct {
	persona  string
	model    string
	baseURL  string
	apiKey   string
	noBash   bool
	noMemory bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	cmd.Flags().StringVar(&f.persona, "persona", "base", "Persona to load from .shell3/personas/")
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
	return cmd
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

	personasDir := filepath.Join(cwd, ".shell3/personas")
	personaName := f.persona

	pCfg, err := persona.ParseConfig(personasDir, personaName)
	if err != nil {
		return err
	}
	if err := persona.Validate(pCfg, personaName); err != nil {
		return err
	}

	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}

	model, baseURL, apiKey, provName := resolveConnection(pCfg.Provider, pCfg.Model, creds, f)

	noBash := pCfg.NoBash || f.noBash
	noMemory := pCfg.NoMemory || f.noMemory

	var st *store.Store
	storeDBPath := filepath.Join(cwd, coalesce(pCfg.DB, ".shell3/shell3.db"))
	if !noMemory {
		if s, err := store.Open(storeDBPath); err == nil {
			st = s
			defer st.Close()
		}
	}

	loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})
	personaData := persona.TemplateData{
		Skills: skills.BuildSection(loadedSkills),
		Time:   time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:    cwd,
		Model:  model,
	}
	pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(pCfg.HooksConfig())

	statusLine := fmt.Sprintf("%s │ %s", provName, model)

	// Parse model pool from credentials for /model command.
	var models []string
	if provCreds, err := creds.Get(provName); err == nil {
		for _, m := range strings.Split(provCreds.DefaultModel, ",") {
			if m := strings.TrimSpace(m); m != "" {
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		models = []string{model}
	}

	client := llm.NewClient(baseURL, apiKey, model)
	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pCfg.Name,
		Models:        models,
		ModelSwitcher: client.SetModel,
		Docs:          docsContent,
	}

	if initialInput != "" {
		return chat.RunOnce(ctx, cfg, initialInput)
	}
	return chat.RunInteractive(ctx, cfg)
}

func resolveConnection(providerHint, modelHint string, creds *config.Credentials, f *runFlags) (model, baseURL, apiKey, provName string) {
	if f.baseURL != "" && f.apiKey != "" {
		return coalesce(f.model, modelHint, "llama3.2"), f.baseURL, f.apiKey, ""
	}

	var provCreds config.ProviderCredentials
	if providerHint != "" {
		if p, ok := creds.Providers[providerHint]; ok {
			provName = providerHint
			provCreds = p
		}
	}

	if provName == "" && len(creds.Providers) > 0 {
		names := make([]string, 0, len(creds.Providers))
		for n := range creds.Providers {
			names = append(names, n)
		}
		sort.Strings(names)
		provName = names[0]
		provCreds = creds.Providers[provName]
	}

	if f.baseURL != "" {
		baseURL = f.baseURL
	} else {
		baseURL = provCreds.BaseURL
	}
	if f.apiKey != "" {
		apiKey = f.apiKey
	} else {
		apiKey = provCreds.APIKey
	}

	model = modelHint
	if f.model != "" {
		model = f.model
	}
	if model == "" {
		for _, part := range strings.Split(provCreds.DefaultModel, ",") {
			if m := strings.TrimSpace(part); m != "" {
				model = m
				break
			}
		}
	}
	if model == "" {
		model = "llama3.2"
	}
	return
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
