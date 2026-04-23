// Package scaffold creates the .shell3/ project directory structure.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const defaultConfig = `# shell3 project configuration
model: llama3.2
provider: ollama
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ""
  on_context_build: ""
`

const codeConfig = `# shell3 code agent configuration
model: llama3.2
provider: ollama
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ""
  on_context_build: ""
`

const defaultGitignore = `memory.db
history.md
`

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

// InitCodeProject scaffolds a .shell3/ directory tuned for shell3 code.
func InitCodeProject(projectDir string) error {
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
		filepath.Join(shell3Dir, "config.yaml"): codeConfig,
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

	fmt.Printf("Initialized .shell3/ (code agent) in %s\n", projectDir)
	fmt.Println("Next: run `shell3 auth` to configure your LLM credentials.")
	return nil
}

// InitProject scaffolds a .shell3/ directory under projectDir with sane defaults.
func InitProject(projectDir string) error {
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
		filepath.Join(shell3Dir, "config.yaml"): defaultConfig,
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

	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	fmt.Println("Next: run `shell3 auth` to configure your LLM credentials.")
	return nil
}
