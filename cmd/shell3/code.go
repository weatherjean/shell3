package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/codeagent"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/scaffold"
)

func newCodeCommand() *cobra.Command {
	var doInit bool
	var model, baseURL, apiKey string

	cmd := &cobra.Command{
		Use:   "code",
		Short: "Interactive coding assistant",
		RunE: func(cmd *cobra.Command, args []string) error {
			if doInit {
				return runCodeInit()
			}
			return runCodeLoop(cmd, model, baseURL, apiKey)
		},
	}
	cmd.Flags().BoolVar(&doInit, "init", false, "Check dependencies and print install prompt")
	cmd.Flags().StringVar(&model, "model", "", "Model override")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key override")
	return cmd
}

func runCodeInit() error {
	cwd, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()
	if err := scaffold.InitCodeProject(cwd, homeDir); err != nil {
		return err
	}
	fmt.Println()

	deps := codeagent.CheckDeps()

	fmt.Println("Checking shell3 code dependencies...")
	fmt.Println()

	for _, d := range deps {
		mark := "✓"
		if !d.Found {
			mark = "✗"
		}
		req := ""
		if d.Required {
			req = " [required]"
		}
		fmt.Printf("  %s %-10s %-12s %s%s\n", mark, d.Name, "("+d.Command+")", d.Description, req)
	}

	fmt.Println()
	installPrompt := codeagent.FormatInstallPrompt(deps)
	fmt.Printf("prompt: %s\n", installPrompt)
	return nil
}

func runCodeLoop(cmd *cobra.Command, modelFlag, baseURLFlag, apiKeyFlag string) error {
	cwd, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()

	// Flags-only mode: bypass all config.
	if baseURLFlag != "" && apiKeyFlag != "" && modelFlag != "" {
		models := parseModels(modelFlag)
		client := llm.NewClient(baseURLFlag, apiKeyFlag, models[0])
		return codeagent.Run(cmd.Context(), codeagent.Config{
			LLM:           client,
			WorkDir:       cwd,
			Model:         models[0],
			Models:        models,
			ModelSwitcher: client.SetModel,
		})
	}

	// Load project config for preferred provider/model (optional — no error if missing).
	projCfg, _ := config.LoadProject(cwd)

	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}

	// Resolve provider: flag > project config > interactive picker.
	provName, provCreds, err := resolveProviderWithHint(creds, baseURLFlag, apiKeyFlag, projectProvider(projCfg))
	if err != nil {
		return err
	}

	// Available models pool always comes from credentials (comma-sep default_model).
	models := parseModels(provCreds.DefaultModel)
	if len(models) == 0 || (len(models) == 1 && models[0] == "") {
		models = []string{promptModel()}
	}

	// Starting model: flag > project config > first in pool.
	startModel := modelFlag
	if startModel == "" && projCfg != nil {
		startModel = projCfg.Model
	}
	if startModel == "" {
		startModel = models[0]
	}

	client := llm.NewClient(provCreds.BaseURL, provCreds.APIKey, startModel)
	return codeagent.Run(cmd.Context(), codeagent.Config{
		LLM:           client,
		WorkDir:       cwd,
		Provider:      provName,
		Model:         startModel,
		Models:        models,
		ModelSwitcher: client.SetModel,
	})
}

// parseModels splits a comma-separated model string into a trimmed slice.
// Always returns at least one non-empty entry.
func parseModels(s string) []string {
	parts := strings.Split(s, ",")
	var models []string
	for _, p := range parts {
		if m := strings.TrimSpace(p); m != "" {
			models = append(models, m)
		}
	}
	if len(models) == 0 {
		return []string{s}
	}
	return models
}

// projectProvider returns the provider name from project config, or "" if none.
func projectProvider(cfg *config.ProjectConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Provider
}

// resolveProviderWithHint picks provider credentials using an optional hint from project config.
func resolveProviderWithHint(creds *config.Credentials, baseURLFlag, apiKeyFlag, hint string) (string, config.ProviderCredentials, error) {
	if baseURLFlag != "" && apiKeyFlag != "" {
		return "", config.ProviderCredentials{BaseURL: baseURLFlag, APIKey: apiKeyFlag}, nil
	}

	if len(creds.Providers) == 0 {
		return "", config.ProviderCredentials{}, fmt.Errorf("no providers configured — run: shell3 auth")
	}

	// Project config hint — use it directly if it matches a known provider.
	if hint != "" {
		if p, ok := creds.Providers[hint]; ok {
			return hint, p, nil
		}
	}

	// Sort for stable display.
	names := make([]string, 0, len(creds.Providers))
	for name := range creds.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 1 {
		return names[0], creds.Providers[names[0]], nil
	}

	// Multiple providers — let user pick.
	chosen := pickProvider(names)
	return chosen, creds.Providers[chosen], nil
}

// pickProvider shows a numbered list and returns the chosen name. Enter = first.
func pickProvider(names []string) string {
	fmt.Println("Select provider (enter for first):")
	for i, name := range names {
		fmt.Printf("  %d. %s\n", i+1, name)
	}
	fmt.Printf("> ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		for i, name := range names {
			if input == fmt.Sprintf("%d", i+1) || strings.EqualFold(input, name) {
				return name
			}
		}
	}
	return names[0]
}

// promptModel asks for a model name with a sensible default.
func promptModel() string {
	fmt.Printf("Model [llama3.2]: ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		if m := strings.TrimSpace(scanner.Text()); m != "" {
			return m
		}
	}
	return "llama3.2"
}
