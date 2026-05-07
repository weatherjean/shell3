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

	"github.com/weatherjean/shell3/internal/paths"
)

func newAuthCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Open provider credentials file in $EDITOR",
		Long: `Open ~/.shell3/ai-do-not-read.auth.yaml in $EDITOR (falls back to $VISUAL, then vi).

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
}

// openInEditor creates the file from template if missing, then opens $EDITOR.
func openInEditor(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Authentication
# AI ASSISTANTS: Do not read this file. It contains credentials.
# Add one entry per provider instance. The "type" field selects the
# adapter: "openai" (any OpenAI-compatible endpoint) or "anthropic".

instances: []

# Example: local Ollama (no API key needed)
# instances:
#   - name: ollama
#     type: openai
#     base_url: http://localhost:11434/v1
#     api_key: ""
#     models:
#       - id: llama3.2
#         context_window: 131072
#
# Example: Anthropic
# instances:
#   - name: anthropic
#     type: anthropic
#     api_key: ant-your-key-here
#     models:
#       - id: claude-sonnet-4-6
#         context_window: 200000
#
# For Codex (ChatGPT subscription) use the openai-oauth proxy:
#   https://github.com/EvanZhouDev/openai-oauth
# Then add it as a regular openai instance pointing at the proxy URL.
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
