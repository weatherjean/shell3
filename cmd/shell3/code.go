package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/codeagent"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
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
			req = " (required)"
		}
		fmt.Printf("  %s %s (%s)%s\n", mark, d.Name, d.Command, req)
	}

	fmt.Println()
	prompt := codeagent.FormatInstallPrompt(deps, codeagent.DetectOS())
	fmt.Println(prompt)
	return nil
}

func runCodeLoop(cmd *cobra.Command, modelFlag, baseURLFlag, apiKeyFlag string) error {
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

	provCreds, _ := creds.Get(projCfg.Provider)

	model := projCfg.Model
	if modelFlag != "" {
		model = modelFlag
	}
	baseURL := provCreds.BaseURL
	if baseURLFlag != "" {
		baseURL = baseURLFlag
	}
	apiKey := provCreds.APIKey
	if apiKeyFlag != "" {
		apiKey = apiKeyFlag
	}

	client := llm.NewClient(baseURL, apiKey, model)
	cfg := codeagent.Config{
		LLM:     client,
		WorkDir: cwd,
	}

	return codeagent.Run(cmd.Context(), cfg)
}
