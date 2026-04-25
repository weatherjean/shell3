// Package scaffold creates the .shell3/ project directory structure.
package scaffold

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/store"
	"gopkg.in/yaml.v3"
)

const defaultGitignore = `memory.db
history.md
shell3.db
`

func buildConfig(provider, model, personality string) string {
	return fmt.Sprintf(`# shell3 project configuration
model: %s
provider: %s
personality: %s
store_db: .shell3/shell3.db
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ""
  on_context_build: ""
`, model, provider, personality)
}

// pickPersonality prompts the user to choose a personality. Returns "code" or "agent".
func pickPersonality() string {
	fmt.Println("Select personality:")
	fmt.Println("  1. code  — coding assistant with bash and memory tools")
	fmt.Println("  2. agent — general agent with bash, memory, and skills")
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		switch strings.TrimSpace(scanner.Text()) {
		case "2", "agent":
			return "agent"
		}
	}
	return "code"
}

// checkExisting returns true and prints a status message if .shell3/config.yaml already exists.
func checkExisting(shell3Dir string) (exists bool) {
	cfgPath := filepath.Join(shell3Dir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}

	fmt.Printf("Configuration already exists: %s\n", cfgPath)

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("  ✗ config.yaml is invalid YAML: %v\n", err)
		return true
	}

	required := []string{"model", "provider"}
	ok := true
	for _, key := range required {
		if v, exists := cfg[key]; !exists || v == "" {
			fmt.Printf("  ✗ missing required field: %s\n", key)
			ok = false
		}
	}
	if ok {
		fmt.Printf("  ✓ model:    %v\n", cfg["model"])
		fmt.Printf("  ✓ provider: %v\n", cfg["provider"])
	}
	fmt.Println("  Run `shell3 destroy` to reset and re-init.")
	return true
}

func initShell3Dir(projectDir, provider, model, personality string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	if checkExisting(shell3Dir) {
		return nil
	}
	dirs := []string{
		shell3Dir,
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}
	files := map[string]string{
		filepath.Join(shell3Dir, "config.yaml"): buildConfig(provider, model, personality),
		filepath.Join(shell3Dir, ".gitignore"):  defaultGitignore,
	}
	for path, content := range files {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("scaffold: write %s: %w", path, err)
		}
	}
	dbPath := filepath.Join(shell3Dir, "shell3.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		st, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("scaffold: create store: %w", err)
		}
		st.Close()
	}
	return nil
}

// InitProject scaffolds a .shell3/ directory under projectDir.
// Requires credentials to exist in homeDir — run `shell3 auth` first.
func InitProject(projectDir, homeDir string) error {
	provider, model, err := firstProviderModel(homeDir)
	if err != nil {
		return err
	}
	personality := pickPersonality()
	if err := initShell3Dir(projectDir, provider, model, personality); err != nil {
		return err
	}
	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	fmt.Printf("  provider:    %s\n  model:       %s\n  personality: %s\n", provider, model, personality)
	return nil
}

// firstProviderModel loads credentials and returns the first provider name and first model.
func firstProviderModel(homeDir string) (provider, model string, err error) {
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return "", "", fmt.Errorf("run `shell3 auth` before `shell3 init`: %w", err)
	}
	name, provCreds, ok := creds.First()
	if !ok {
		return "", "", fmt.Errorf("no providers configured — run: shell3 auth")
	}
	// Use first model from comma-sep list.
	m := provCreds.DefaultModel
	for _, part := range splitComma(m) {
		if part != "" {
			m = part
			break
		}
	}
	return name, m, nil
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if p := trim(s[start:i]); p != "" {
				out = append(out, p)
			}
			start = i + 1
		}
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
