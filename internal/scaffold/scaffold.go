// Package scaffold creates the .shell3/ project directory structure.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultConfig = `# shell3 project configuration
model: llama3.2
provider: ollama
default_personality: coder
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ""
  on_context_build: ""
`

const defaultGitignore = `memory.db
history.md
`

const defaultCoderPersonality = `name: coder
model: llama3.2
provider: ollama
system_prompt: |
  You are an expert software engineer working in the project directory.
  Use the bash tool to read files, run tests, and make changes.
  Work methodically: read before writing, test after changing.
tools:
  - bash
  - memory_search
  - memory_store
`

// InitProject scaffolds a .shell3/ directory under projectDir with sane defaults.
func InitProject(projectDir string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	dirs := []string{
		shell3Dir,
		filepath.Join(shell3Dir, "personalities"),
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(shell3Dir, "config.yaml"):                       defaultConfig,
		filepath.Join(shell3Dir, ".gitignore"):                        defaultGitignore,
		filepath.Join(shell3Dir, "personalities", "coder.yaml"):       defaultCoderPersonality,
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
