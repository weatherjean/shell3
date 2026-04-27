package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/secrets"
)

func newSecretsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage project tool secrets",
		Long: `Manage project tool secrets.

Secrets live in the obfuscated store at .shell3/secrets.shell3 (project-
scoped). They are exposed only to user tools that declare the matching
name in their tool YAML's "secrets:" field.

Operations:
  shell3 secrets set --key NAME --secret VALUE   write or overwrite one secret
  shell3 secrets list                             list names with last 3 chars masked
  shell3 secrets remove --key NAME                delete one secret`,
	}
	cmd.AddCommand(newSecretsSetCommand())
	cmd.AddCommand(newSecretsListCommand())
	cmd.AddCommand(newSecretsRemoveCommand())
	return cmd
}

func newSecretsSetCommand() *cobra.Command {
	var key, secret string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set or overwrite a secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return fmt.Errorf("--key is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			if err := s.Set(key, secret); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s\n", key)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Secret name (e.g. BRAVE_API_KEY)")
	cmd.Flags().StringVar(&secret, "secret", "", "Secret value")
	return cmd
}

func newSecretsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured secret names (values masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			return runSecretsList(s, cmd.OutOrStdout())
		},
	}
}

func newSecretsRemoveCommand() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return fmt.Errorf("--key is required")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			if err := s.Remove(key); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", key)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Secret name to remove")
	return cmd
}

func runSecretsList(s *secrets.Store, out io.Writer) error {
	names := s.List()
	if len(names) == 0 {
		fmt.Fprintln(out, "No secrets configured. Run: shell3 secrets set --key NAME --secret VALUE")
		return nil
	}
	all := s.All()
	fmt.Fprintf(out, "%-32s  %s\n", "NAME", "VALUE")
	for _, name := range names {
		fmt.Fprintf(out, "%-32s  %s\n", name, maskSecret(all[name]))
	}
	return nil
}

// maskSecret returns the value with its last 3 characters replaced by
// asterisks. Values shorter than 4 characters are entirely masked.
func maskSecret(v string) string {
	if len(v) <= 3 {
		return "***"
	}
	return v[:len(v)-3] + "***"
}
