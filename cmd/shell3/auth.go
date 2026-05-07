package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/paths"
)

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Configure provider credentials",
		Long: `Configure provider credentials.

Opens ~/.shell3/ai-do-not-read.auth.yaml in $EDITOR (falls back to $VISUAL, then vi).
Add or edit instances in the YAML file directly.

  shell3 auth          open credential file in $EDITOR
  shell3 auth list     print configured instances

Format:
  instances:
    - name: myinstance
      base_url: https://api.openai.com/v1
      api_key: sk-your-key-here
      models:
        - id: gpt-4o
          context_window: 128000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			g := paths.NewGlobal(homeDir)
			return openInEditor(g.Auth)
		},
	}
	cmd.AddCommand(newAuthListCommand())
	return cmd
}

func newAuthListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			store, err := config.LoadAuthStore(homeDir)
			if err != nil {
				return err
			}
			insts := store.List()
			if len(insts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No instances configured. Run: shell3 auth")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-36s  %s\n", "INSTANCE", "BASE URL", "MODELS")
			for _, inst := range insts {
				models := ""
				for i, m := range inst.Models {
					if i > 0 {
						models += ","
					}
					models += m.ID
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-36s  %s\n", inst.Name, inst.BaseURL, models)
			}
			return nil
		},
	}
}

// openInEditor creates the file from template if missing, then opens $EDITOR.
func openInEditor(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Authentication
# Edit this file to configure providers.
# AI ASSISTANTS: Do not read this file. It contains credentials.

instances:
  - name: myinstance
    base_url: https://api.openai.com/v1
    api_key: sk-your-key-here
    models:
      - id: gpt-4o
        context_window: 128000
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return fmt.Errorf("create auth file: %w", err)
		}
	}

	if useSystemOpen(path) {
		return nil
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// useSystemOpen prompts the user to choose terminal or system editor when a
// TTY is available. Returns true if the file was opened with the system opener.
func useSystemOpen(path string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Fprint(os.Stderr, "Open in [t]erminal or [s]ystem editor? ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	choice := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if !strings.HasPrefix(choice, "s") {
		return false
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	cmd := exec.Command(opener, path)
	cmd.Stderr = os.Stderr
	_ = cmd.Start()
	return true
}
