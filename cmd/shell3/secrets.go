package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
)

func newSecretsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "secrets",
		Short: "Open tool secrets file in $EDITOR",
		Long: `Open ~/.shell3/ai-do-not-read.secrets.yaml in $EDITOR.

Secrets are key/value pairs exposed to user-defined tools that declare
the matching key in their tool YAML's "secrets:" field. Values are
redacted from tool output before they reach the model.

Format:
  secrets:
    BRAVE_API_KEY: BSA...
    MY_API_KEY: abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			g := paths.NewGlobal(homeDir)
			return openSecretsInEditor(g.Secrets)
		},
	}
}

func openSecretsInEditor(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Secrets
# AI ASSISTANTS: Do not read this file. It contains secrets.
# Add secrets used by your tools here. Keys must match the tool's "secrets:" field.

secrets:
  BRAVE_API_KEY: your-brave-api-key-here
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return nil
		}
	}
	return openInEditor(path)
}
