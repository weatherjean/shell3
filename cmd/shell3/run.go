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
	"github.com/weatherjean/shell3/internal/usertools"
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

	envPath := filepath.Join(cwd, ".shell3", ".env")
	dotEnv, dotEnvErr := usertools.LoadDotEnv(envPath)
	if dotEnvErr != nil {
		fmt.Fprintln(os.Stderr, "warning:", dotEnvErr)
	}
	secrets := map[string]string{}
	for k, v := range dotEnv {
		secrets[k] = v
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		secrets[kv[:eq]] = kv[eq+1:]
	}
	available := map[string]struct{}{}
	for k := range secrets {
		available[k] = struct{}{}
	}

	toolsDirs := []string{
		filepath.Join(homeDir, ".shell3", "tools"),
		filepath.Join(cwd, ".shell3", "tools"),
	}
	loadedTools, toolWarnings, _ := usertools.LoadAll(toolsDirs, available)
	for _, w := range toolWarnings {
		fmt.Fprintln(os.Stderr, "user-tool warning:", w)
	}
	userToolDefs := make([]llm.ToolDefinition, 0, len(loadedTools))
	userToolMap := make(map[string]usertools.Tool, len(loadedTools))
	for _, ut := range loadedTools {
		userToolDefs = append(userToolDefs, llm.ToolDefinition{
			Name:        ut.Name,
			Description: ut.Description,
			Parameters:  ut.Parameters,
		})
		userToolMap[ut.Name] = ut
	}

	var coreMemories []store.MemoryEntry
	if st != nil {
		mems, err := st.MemoryQuery("", true, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: load core memories:", err)
		} else {
			coreMemories = mems
			var bytes int
			for _, m := range mems {
				bytes += len(m.Key) + len(m.Value) + 4
			}
			if bytes > 2048 {
				fmt.Fprintf(os.Stderr,
					"warning: core memories total %d bytes (>2KB), consider demoting some\n",
					bytes)
			}
		}
	}

	personaData := persona.TemplateData{
		Skills:       skills.BuildSection(loadedSkills),
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          cwd,
		Model:        model,
		CoreMemories: coreMemories,
	}
	pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash, userToolDefs)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(pCfg.Config)

	statusLine := fmt.Sprintf("%s │ %s", provName, model)

	// Aggregate models across every configured provider for /model picker.
	var models []chat.ModelChoice
	provNames := make([]string, 0, len(creds.Providers))
	for n := range creds.Providers {
		provNames = append(provNames, n)
	}
	sort.Strings(provNames)
	for _, n := range provNames {
		pc := creds.Providers[n]
		for _, m := range strings.Split(pc.DefaultModel, ",") {
			if m := strings.TrimSpace(m); m != "" {
				models = append(models, chat.ModelChoice{Provider: n, Model: m})
			}
		}
	}
	// Append models from any self-registered providers (e.g. OAuth-based)
	// whose entries do not live in credentials.yaml. Skip duplicates of
	// providers already configured as API-key providers.
	seen := map[string]bool{}
	for _, n := range provNames {
		seen[n] = true
	}
	regNames := llm.Registered()
	sort.Strings(regNames)
	for _, n := range regNames {
		if seen[n] {
			continue
		}
		p, ok := llm.Get(n)
		if !ok {
			continue
		}
		for _, m := range p.Models() {
			models = append(models, chat.ModelChoice{Provider: n, Model: m})
		}
	}
	if len(models) == 0 {
		models = []chat.ModelChoice{{Provider: provName, Model: model}}
	}

	// buildClient picks a Streamer for the given (provider, model). Uses the
	// llm registry first (OAuth-style providers), then falls back to the
	// OpenAI-compatible client backed by credentials.yaml.
	buildClient := func(p, m string) (chat.LLMClient, error) {
		if reg, ok := llm.Get(p); ok {
			s, err := reg.NewClient(ctx, m)
			if err != nil {
				return nil, err
			}
			return s, nil
		}
		pc, err := creds.Get(p)
		if err != nil {
			return nil, err
		}
		return llm.NewClient(pc.BaseURL, pc.APIKey, m), nil
	}

	var client chat.LLMClient
	var openaiClient *llm.Client // non-nil only for credentials.yaml-backed providers
	if _, ok := llm.Get(provName); ok {
		s, err := buildClient(provName, model)
		if err != nil {
			return err
		}
		client = s
	} else {
		openaiClient = llm.NewClient(baseURL, apiKey, model)
		client = openaiClient
	}

	modelSwitcher := func(provider, modelName string) (chat.LLMClient, error) {
		if provider == "" || provider == provName {
			// Same provider: cheap path for credentials.yaml-backed clients
			// is to mutate the model in place. Registry-backed clients
			// rebuild — model is captured at construction time.
			if openaiClient != nil {
				openaiClient.SetModel(modelName)
				model = modelName
				return nil, nil
			}
		}
		s, err := buildClient(provider, modelName)
		if err != nil {
			return nil, err
		}
		// Track the active concrete client for in-place SetModel only when
		// the new client is the OpenAI-compatible one.
		if oc, ok := s.(*llm.Client); ok {
			openaiClient = oc
		} else {
			openaiClient = nil
		}
		client = s
		provName = provider
		model = modelName
		return s, nil
	}
	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pCfg.Name,
		Models:        models,
		ModelSwitcher: modelSwitcher,
		Docs:          docsContent,
		UserTools:     userToolMap,
		Secrets:       secrets,
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
		} else if _, ok := llm.Get(providerHint); ok {
			// Self-registered provider (OAuth-based). It owns its own auth
			// and has no credentials.yaml entry; surface its model verbatim
			// and leave baseURL/apiKey empty so the caller routes through
			// the registry.
			provName = providerHint
			model = modelHint
			if f.model != "" {
				model = f.model
			}
			return model, "", "", provName
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
