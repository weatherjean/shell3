package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

func newAuthCommand() *cobra.Command {
	var providerFlag string
	var instanceFlag string

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Configure adapter credentials",
		Long: `Configure adapter credentials.

Operations:
  shell3 auth                                       interactive: pick adapter + configure instance
  shell3 auth --provider=openai --instance=NAME     non-interactive: configure one instance
  shell3 auth --provider=codex                      OAuth browser flow (single-instance adapter)

  shell3 auth list                                  list configured instances
  shell3 auth remove INSTANCE                       delete one instance
  shell3 auth models INSTANCE                       show default_model CSV
  shell3 auth models INSTANCE "a,b,c"               replace default_model CSV
  shell3 auth models INSTANCE ""                    clear; adapter built-in list applies

Concepts:
  adapter   code path for a backend (openai, codex)
  instance  one named credential set; openai supports many, codex always "codex"
  models    comma-separated default_model list cycled by /model in the TUI

Storage: ~/.shell3/credentials.shell3 (XOR-obfuscated; not encrypted).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			if err := config.Migrate(homeDir); err != nil {
				return err
			}
			store, err := config.LoadCredStore(homeDir)
			if err != nil {
				return err
			}

			adapter := providerFlag
			if adapter == "" {
				adapter = pickAdapter(os.Stdin, os.Stdout)
				if adapter == "" {
					return fmt.Errorf("no adapter chosen")
				}
			}
			p, ok := llm.Get(adapter)
			if !ok {
				return fmt.Errorf("unknown adapter %q (registered: %v)", adapter, llm.Registered())
			}

			instance := instanceFlag
			if p.SingleInstance() {
				instance = p.Name()
			} else if instance == "" {
				instance = promptInstance(os.Stdin, os.Stdout, p.Name())
			}

			return p.Auth(cmd.Context(), os.Stdout, store, instance)
		},
	}
	cmd.Flags().StringVar(&providerFlag, "provider", "", "Adapter name (e.g. openai, codex)")
	cmd.Flags().StringVar(&instanceFlag, "instance", "", "Instance name (multi-instance adapters)")

	cmd.AddCommand(newAuthListCommand())
	cmd.AddCommand(newAuthRemoveCommand())
	cmd.AddCommand(newAuthModelsCommand())
	return cmd
}

func newAuthListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := loadStoreForAuth()
			if err != nil {
				return err
			}
			return runAuthList(store, os.Stdout)
		},
	}
}

func newAuthRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove INSTANCE",
		Short: "Delete a configured instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := loadStoreForAuth()
			if err != nil {
				return err
			}
			return runAuthRemove(store, args[0], os.Stdout)
		},
	}
}

func newAuthModelsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "models INSTANCE [CSV]",
		Short: "Show or replace the default_model CSV for an instance",
		Long: `Show or replace the default_model CSV for an instance.

Examples:
  shell3 auth models codex                       show current models
  shell3 auth models codex "gpt-5.1,gpt-5.2"     replace list with two models
  shell3 auth models codex ""                    clear; adapter falls back to its built-in list`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := loadStoreForAuth()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return runAuthModelsShow(store, args[0], os.Stdout)
			}
			return runAuthModelsSet(store, args[0], args[1], os.Stdout)
		},
	}
}

func loadStoreForAuth() (*config.CredStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if err := config.Migrate(homeDir); err != nil {
		return nil, err
	}
	return config.LoadCredStore(homeDir)
}

func runAuthList(store *config.CredStore, out io.Writer) error {
	list := store.List()
	if len(list) == 0 {
		_, _ = fmt.Fprintln(out, "No instances configured. Run: shell3 auth")
		return nil
	}
	_, _ = fmt.Fprintf(out, "%-24s  %-12s  %s\n", "INSTANCE", "ADAPTER", "MODELS")
	for _, m := range list {
		_, fields, _ := store.Get(m.Instance)
		models := fields["default_model"]
		if models == "" {
			if p, ok := llm.Get(m.Adapter); ok {
				models = strings.Join(p.Models(store, m.Instance), ",")
			}
		}
		_, _ = fmt.Fprintf(out, "%-24s  %-12s  %s\n", m.Instance, m.Adapter, models)
	}
	return nil
}

func runAuthRemove(store *config.CredStore, instance string, out io.Writer) error {
	if _, _, ok := store.Get(instance); !ok {
		return fmt.Errorf("no instance %q (run: shell3 auth list)", instance)
	}
	if err := store.Delete(instance); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Removed instance %q.\n", instance)
	return nil
}

func runAuthModelsShow(store *config.CredStore, instance string, out io.Writer) error {
	_, fields, ok := store.Get(instance)
	if !ok {
		return fmt.Errorf("no instance %q (run: shell3 auth list)", instance)
	}
	current := fields["default_model"]
	if current == "" {
		_, _ = fmt.Fprintf(out, "%s: (none stored; adapter built-in list applies)\n", instance)
	} else {
		_, _ = fmt.Fprintf(out, "%s: %s\n", instance, current)
	}
	return nil
}

func runAuthModelsSet(store *config.CredStore, instance, csv string, out io.Writer) error {
	if _, _, ok := store.Get(instance); !ok {
		return fmt.Errorf("no instance %q (run: shell3 auth list)", instance)
	}
	csv = strings.TrimSpace(csv)
	if err := store.Update(instance, func(f map[string]string) error {
		f["default_model"] = csv
		return nil
	}); err != nil {
		return err
	}
	if csv == "" {
		_, _ = fmt.Fprintf(out, "Cleared models for %q. Adapter built-in list will apply.\n", instance)
	} else {
		_, _ = fmt.Fprintf(out, "Set models for %q: %s\n", instance, csv)
	}
	return nil
}

func pickAdapter(in io.Reader, out io.Writer) string {
	names := llm.Registered()
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	_, _ = fmt.Fprintln(out, "Available adapters:")
	for i, n := range names {
		p, _ := llm.Get(n)
		marker := ""
		if p.SingleInstance() {
			marker = " (single-instance)"
		}
		_, _ = fmt.Fprintf(out, "  %d) %s%s\n", i+1, n, marker)
	}
	_, _ = fmt.Fprint(out, "Pick an adapter [1]: ")
	s := bufio.NewScanner(in)
	if !s.Scan() {
		return ""
	}
	choice := strings.TrimSpace(s.Text())
	if choice == "" {
		return names[0]
	}
	for i, n := range names {
		if choice == fmt.Sprintf("%d", i+1) || choice == n {
			return n
		}
	}
	return ""
}

func promptInstance(in io.Reader, out io.Writer, defaultName string) string {
	_, _ = fmt.Fprintf(out, "Instance name [%s] (for your later reference): ", defaultName)
	s := bufio.NewScanner(in)
	if !s.Scan() {
		return defaultName
	}
	v := strings.TrimSpace(s.Text())
	if v == "" {
		return defaultName
	}
	return v
}
