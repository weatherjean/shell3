package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/secrets"
)

func newSecretsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage global tool secrets",
		Long: `Manage global tool secrets.

Opens ~/.shell3/ai-do-not-read.secrets.yaml in $EDITOR.
Secrets are exposed to user tools that declare the matching key in their
tool YAML's "secrets:" field.

  shell3 secrets        open secrets file in $EDITOR
  shell3 secrets list   list names (values masked)

Format:
  secrets:
    GITHUB_TOKEN: ghp_...
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
	cmd.AddCommand(newSecretsListCommand())
	return cmd
}

func newSecretsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured secret names (values masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			s, err := secrets.Load(homeDir)
			if err != nil {
				return err
			}
			return runSecretsList(s, cmd.OutOrStdout())
		},
	}
}

func runSecretsList(s *secrets.Store, out io.Writer) error {
	names := s.List()
	if len(names) == 0 {
		fmt.Fprintln(out, "No secrets configured. Run: shell3 secrets")
		return nil
	}
	all := s.All()
	fmt.Fprintf(out, "%-32s  %s\n", "NAME", "VALUE")
	for _, name := range names {
		fmt.Fprintf(out, "%-32s  %s\n", name, maskSecret(all[name]))
	}
	return nil
}

func maskSecret(v string) string {
	if len(v) <= 3 {
		return "***"
	}
	return v[:len(v)-3] + "***"
}

func openSecretsInEditor(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Secrets
# AI ASSISTANTS: Do not read this file. It contains secrets.

secrets:
  BRAVE_API_KEY: your-brave-api-key-here
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return fmt.Errorf("create secrets file: %w", err)
		}
	}
	return openInEditor(path)
}
