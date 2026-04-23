# shell3 code Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `shell3 code` — an interactive coding REPL where the model outputs `bash` blocks that the agent executes, with `--init` for dependency checking and install prompt generation.

**Architecture:** `shell3 code` skips OpenAI tool_calls entirely. Instead, the system prompt instructs the model to emit ` ```bash ` blocks. The loop streams the response to stdout, extracts bash blocks from the completed response, executes each with `exec.CommandContext`, feeds stdout+stderr back as a user message, and repeats until the model produces no bash blocks. Each turn gets a fresh `context.WithCancel`; ctrl+c fires `cancel()`, killing both the in-flight HTTP request and any running subprocess. Input is read via `gum input` if available, plain readline otherwise.

**Tech Stack:** Go 1.25+, `os/exec`, `os/signal`, `context`, `bufio`, `github.com/spf13/cobra`, existing `internal/llm`, existing `internal/config`

---

## File Map

```
cmd/shell3/code.go              create  cobra subcommand wiring for `shell3 code`
internal/codeagent/
  init.go                       create  CheckDeps(), FormatInstallPrompt(), detectOS()
  prompt.go                     create  CodeSystemPrompt() string
  input.go                      create  ReadInput() — gum or plain readline
  loop.go                       create  Run(), extractBashBlocks(), executeBlock()
  loop_test.go                  create  unit tests for bash block extraction
  init_test.go                  create  unit tests for dep check and prompt formatting
```

---

## Task 1: Dependency Checker (`--init`)

**Files:**
- Create: `internal/codeagent/init.go`
- Create: `internal/codeagent/init_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/codeagent/init_test.go`:

```go
package codeagent_test

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/codeagent"
)

func TestCheckDeps_GitPresent(t *testing.T) {
	deps := codeagent.CheckDeps()
	var git *codeagent.DepStatus
	for i := range deps {
		if deps[i].Command == "git" {
			git = &deps[i]
		}
	}
	if git == nil {
		t.Fatal("expected git in dep list")
	}
	if !git.Found {
		t.Error("git should be found on any dev machine")
	}
	if !git.Required {
		t.Error("git should be required")
	}
}

func TestCheckDeps_FakeNotFound(t *testing.T) {
	dep := codeagent.LookupDep("definitely-not-a-real-tool-xyz", false)
	if dep.Found {
		t.Error("fake tool should not be found")
	}
}

func TestFormatInstallPrompt_NothingMissing(t *testing.T) {
	deps := []codeagent.DepStatus{
		{Name: "git", Command: "git", Found: true, Required: true},
	}
	prompt := codeagent.FormatInstallPrompt(deps, "macos")
	if strings.Contains(prompt, "brew install") {
		t.Error("no install commands expected when nothing missing")
	}
	if !strings.Contains(prompt, "All") {
		t.Errorf("expected 'All' message, got: %q", prompt)
	}
}

func TestFormatInstallPrompt_Missing(t *testing.T) {
	deps := []codeagent.DepStatus{
		{Name: "ripgrep", Command: "rg", Found: false, Required: false},
		{Name: "gum", Command: "gum", Found: false, Required: false},
	}
	prompt := codeagent.FormatInstallPrompt(deps, "macos")
	if !strings.Contains(prompt, "brew install") {
		t.Errorf("expected brew install command, got: %q", prompt)
	}
	if !strings.Contains(prompt, "ripgrep") {
		t.Error("expected ripgrep in prompt")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/codeagent/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement dep checker**

Create `internal/codeagent/init.go`:

```go
package codeagent

import (
	"fmt"
	"os/exec"
	"strings"
)

// DepStatus is the result of checking one CLI tool.
type DepStatus struct {
	Name     string
	Command  string
	Found    bool
	Required bool
}

// LookupDep checks whether a single command exists in PATH.
func LookupDep(command string, required bool) DepStatus {
	_, err := exec.LookPath(command)
	return DepStatus{Command: command, Found: err == nil, Required: required}
}

// CheckDeps checks all tools shell3 code can use.
func CheckDeps() []DepStatus {
	specs := []struct {
		name     string
		cmd      string
		required bool
	}{
		{"git", "git", true},
		{"ripgrep", "rg", false},
		{"fd", "fd", false},
		{"jq", "jq", false},
		{"gum", "gum", false},
		{"bat", "bat", false},
		{"sd", "sd", false},
		{"yq", "yq", false},
	}
	deps := make([]DepStatus, len(specs))
	for i, s := range specs {
		deps[i] = LookupDep(s.cmd, s.required)
		deps[i].Name = s.name
	}
	return deps
}

// FormatInstallPrompt prints dep status and returns a prompt the user can
// paste into any AI agent to install missing tools.
func FormatInstallPrompt(deps []DepStatus, os string) string {
	var missing []DepStatus
	for _, d := range deps {
		if !d.Found {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return "All shell3 code dependencies are installed."
	}

	names := make([]string, len(missing))
	for i, d := range missing {
		names[i] = d.Name + " (" + d.Command + ")"
	}

	cmds := installCommands(missing, os)

	return fmt.Sprintf(
		"Please install the following tools needed for shell3 code:\n%s\n\n%s",
		"- "+strings.Join(names, "\n- "),
		cmds,
	)
}

func installCommands(missing []DepStatus, os string) string {
	cmds := make([]string, len(missing))
	for i, d := range missing {
		cmds[i] = brewName(d.Command)
	}
	joined := strings.Join(cmds, " ")

	switch os {
	case "macos":
		return "On macOS:\n  brew install " + joined
	case "ubuntu":
		return "On Ubuntu:\n  sudo apt install ripgrep fd-find jq\n  # gum/bat/sd/yq: cargo install or snap\n  # See each tool's README for Ubuntu install"
	default:
		return "Install via your package manager: " + joined
	}
}

func brewName(cmd string) string {
	// fd is named fd in brew but fd-find on apt
	if cmd == "fd" {
		return "fd"
	}
	if cmd == "rg" {
		return "ripgrep"
	}
	return cmd
}

// DetectOS returns "macos", "ubuntu", or "linux".
func DetectOS() string {
	out, err := exec.Command("uname").Output()
	if err != nil {
		return "unknown"
	}
	switch strings.TrimSpace(string(out)) {
	case "Darwin":
		return "macos"
	case "Linux":
		if _, err := exec.LookPath("apt"); err == nil {
			return "ubuntu"
		}
		return "linux"
	}
	return "unknown"
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/codeagent/... -v -run TestCheck
go test ./internal/codeagent/... -v -run TestFormat
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codeagent/init.go internal/codeagent/init_test.go
git commit -m "feat: add codeagent dep checker and install prompt generator"
```

---

## Task 2: System Prompt

**Files:**
- Create: `internal/codeagent/prompt.go`

No test needed — pure string constant. Verified manually via Task 6.

- [ ] **Step 1: Write system prompt**

Create `internal/codeagent/prompt.go`:

```go
package codeagent

// CodeSystemPrompt is the system prompt for the shell3 code assistant.
// The model must output ```bash blocks for commands — no tool_calls API used.
const CodeSystemPrompt = `You are an expert software engineer working in the user's project directory.

## How to act

To read files, search code, run tests, or make changes — output a fenced bash block:

` + "```" + `bash
<command here>
` + "```" + `

The agent will execute the command and show you the output. You can then continue reasoning and issue more commands. When done, respond in plain text with no bash blocks.

## File reading rules

Always check file size before reading. Never cat a file blindly.

Good:
` + "```" + `bash
wc -l internal/agent/loop.go
` + "```" + `
If output is under 150 lines, read in full:
` + "```" + `bash
cat internal/agent/loop.go
` + "```" + `
If 150–500 lines, read in sections:
` + "```" + `bash
sed -n '1,80p' internal/agent/loop.go
` + "```" + `
If over 500 lines, search instead:
` + "```" + `bash
rg 'functionName' internal/agent/loop.go
` + "```" + `

Bad (never do this blindly):
` + "```" + `bash
cat internal/agent/loop.go
` + "```" + `

## Preferred tools

- Search code: ` + "`rg 'pattern' path`" + `
- Find files: ` + "`fd 'pattern'`" + ` or ` + "`find . -name '*.go'`" + `
- List directory: ` + "`ls -la path`" + `
- Read file section: ` + "`sed -n 'START,ENDp' file`" + `
- Search and replace: ` + "`sd 'old' 'new' file`" + ` or ` + "`sed -i 's/old/new/g' file`" + `
- Run tests: ` + "`go test ./...`" + `

## Approach

1. Read before writing — understand existing code first
2. Check file size before reading
3. Make minimal changes — don't refactor beyond the task
4. Run tests after making changes
5. One bash block per logical step — don't chain unrelated commands
`
```

- [ ] **Step 2: Commit**

```bash
git add internal/codeagent/prompt.go
git commit -m "feat: add code assistant system prompt with file-size rules"
```

---

## Task 3: Input Reader

**Files:**
- Create: `internal/codeagent/input.go`

No unit test — depends on stdin/gum. Tested manually in Task 6.

- [ ] **Step 1: Implement input reader**

Create `internal/codeagent/input.go`:

```go
package codeagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ReadInput reads one line from the user.
// Uses gum input if available, plain readline otherwise.
// Returns io.EOF when user wants to exit (ctrl+c or empty gum cancel).
func ReadInput() (string, error) {
	if _, err := exec.LookPath("gum"); err == nil {
		return readGum()
	}
	return readPlain()
}

func readGum() (string, error) {
	out, err := exec.Command("gum", "input", "--placeholder", "Ask shell3...").Output()
	if err != nil {
		// ctrl+c in gum = exit signal
		return "", io.EOF
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", io.EOF
	}
	return text, nil
}

func readPlain() (string, error) {
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			return readPlain()
		}
		return text, nil
	}
	return "", io.EOF
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/codeagent/input.go
git commit -m "feat: add input reader with gum/readline fallback"
```

---

## Task 4: Bash Block Parser and Executor

**Files:**
- Create: `internal/codeagent/loop.go` (partial — parser + executor only)
- Create: `internal/codeagent/loop_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/codeagent/loop_test.go`:

```go
package codeagent_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/codeagent"
)

func TestExtractBashBlocks_None(t *testing.T) {
	blocks := codeagent.ExtractBashBlocks("Just some text with no code blocks.")
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestExtractBashBlocks_One(t *testing.T) {
	text := "I'll check the files.\n```bash\nls -la\n```\nDone."
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0] != "ls -la" {
		t.Errorf("got %q", blocks[0])
	}
}

func TestExtractBashBlocks_Multiple(t *testing.T) {
	text := "```bash\nwc -l foo.go\n```\nThen read it:\n```bash\ncat foo.go\n```"
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0] != "wc -l foo.go" {
		t.Errorf("block 0: got %q", blocks[0])
	}
	if blocks[1] != "cat foo.go" {
		t.Errorf("block 1: got %q", blocks[1])
	}
}

func TestExtractBashBlocks_NonBashFenced(t *testing.T) {
	text := "```go\nfmt.Println()\n```"
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 0 {
		t.Errorf("non-bash fenced block should not be extracted")
	}
}

func TestExecuteBlock_Echo(t *testing.T) {
	out := codeagent.ExecuteBlock(context.Background(), "echo hello", "/tmp")
	if out != "hello\n" {
		t.Errorf("got %q", out)
	}
}

func TestExecuteBlock_ExitError(t *testing.T) {
	out := codeagent.ExecuteBlock(context.Background(), "exit 1", "/tmp")
	if out == "" {
		t.Error("expected non-empty output on exit error")
	}
}

func TestExecuteBlock_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	out := codeagent.ExecuteBlock(ctx, "sleep 10", "/tmp")
	if out == "" {
		t.Error("expected error output for cancelled context")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/codeagent/... -v -run TestExtract
go test ./internal/codeagent/... -v -run TestExecute
```

Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement parser and executor**

Create `internal/codeagent/loop.go` with just the parser and executor (loop added in Task 5):

```go
package codeagent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExtractBashBlocks extracts the contents of all ```bash ... ``` blocks from text.
func ExtractBashBlocks(text string) []string {
	var blocks []string
	parts := strings.Split(text, "```")
	// parts alternate: outside, inside, outside, inside ...
	for i := 1; i < len(parts); i += 2 {
		block := parts[i]
		lang, body, found := strings.Cut(block, "\n")
		if !found {
			continue
		}
		if strings.TrimSpace(lang) != "bash" {
			continue
		}
		trimmed := strings.TrimSpace(body)
		if trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}
	return blocks
}

// ExecuteBlock runs a shell command and returns combined stdout+stderr.
// On error, the error message is appended to the output.
func ExecuteBlock(ctx context.Context, command, workDir string) string {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		if buf.Len() == 0 {
			fmt.Fprintf(&buf, "error: %v\n", err)
		}
	}
	return buf.String()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/codeagent/... -v -run TestExtract
go test ./internal/codeagent/... -v -run TestExecute
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codeagent/loop.go internal/codeagent/loop_test.go
git commit -m "feat: add bash block extractor and executor"
```

---

## Task 5: Code Agent Loop

**Files:**
- Modify: `internal/codeagent/loop.go` (add `Run`, `Config`, `LLMClient`)

- [ ] **Step 1: Add the Run loop to loop.go**

Append to `internal/codeagent/loop.go`:

```go
import (
	// add these to existing imports
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/weatherjean/shell3/internal/llm"
)

// LLMClient is the interface loop.go needs from the LLM layer.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds everything Run needs.
type Config struct {
	LLM     LLMClient
	WorkDir string
}

// Run starts the interactive coding loop. Exits on ctrl+c at the prompt or io.EOF.
// Ctrl+c during an active turn cancels only that turn and returns to the prompt.
func Run(ctx context.Context, cfg Config) error {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: CodeSystemPrompt},
	}

	for {
		input, err := ReadInput()
		if err == io.EOF {
			fmt.Println()
			return nil
		}
		if err != nil {
			return err
		}

		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: input})
		messages = runTurn(ctx, cfg, messages)
		fmt.Println()
	}
}

// runTurn runs one user→assistant exchange, potentially multiple LLM calls
// if the model issues bash blocks. Returns updated message slice.
// ctrl+c cancels the turn and returns messages as-is.
func runTurn(ctx context.Context, cfg Config, messages []llm.Message) []llm.Message {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)
	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-turnCtx.Done():
		}
		signal.Stop(sigChan)
	}()

	for {
		response, cancelled := streamResponse(turnCtx, cfg.LLM, messages)
		if cancelled {
			fmt.Println("\n[cancelled]")
			return messages
		}

		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: response})

		blocks := ExtractBashBlocks(response)
		if len(blocks) == 0 {
			return messages
		}

		var cmdResults strings.Builder
		for _, block := range blocks {
			fmt.Printf("\n$ %s\n", block)
			out := ExecuteBlock(turnCtx, block, cfg.WorkDir)
			if turnCtx.Err() != nil {
				fmt.Println("[cancelled]")
				return messages
			}
			fmt.Print(out)
			fmt.Fprintf(&cmdResults, "$ %s\n%s\n", block, out)
		}

		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: cmdResults.String()})
	}
}

// streamResponse streams one LLM response, printing tokens as they arrive.
// Returns the full response text and whether the context was cancelled.
func streamResponse(ctx context.Context, client LLMClient, messages []llm.Message) (string, bool) {
	var sb strings.Builder
	err := client.Stream(ctx, messages, nil, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			fmt.Print(ev.TextDelta)
			sb.WriteString(ev.TextDelta)
		}
	})
	if err != nil && ctx.Err() != nil {
		return sb.String(), true
	}
	return sb.String(), false
}
```

- [ ] **Step 2: Fix imports — loop.go needs a single import block**

The file now has two import blocks. Merge them into one. Final `loop.go` should have a single `import (...)` block containing all needed packages:

```go
package codeagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/weatherjean/shell3/internal/llm"
)
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
go build ./internal/codeagent/...
```

Expected: no errors.

- [ ] **Step 4: Run all codeagent tests**

```bash
go test ./internal/codeagent/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codeagent/loop.go
git commit -m "feat: add code agent REPL loop with per-turn context cancellation"
```

---

## Task 6: CLI Wiring — `shell3 code`

**Files:**
- Create: `cmd/shell3/code.go`
- Modify: `cmd/shell3/main.go` (add `code` subcommand)

- [ ] **Step 1: Create code subcommand**

Create `cmd/shell3/code.go`:

```go
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
```

- [ ] **Step 2: Add code command to main.go**

Open `cmd/shell3/main.go`. Add `root.AddCommand(newCodeCommand())` after the existing `AddCommand` calls:

```go
root.AddCommand(newInitCommand())
root.AddCommand(newAuthCommand())
root.AddCommand(newCodeCommand())  // add this line
```

- [ ] **Step 3: Build**

```bash
go build -o shell3 ./cmd/shell3/
```

Expected: no errors.

- [ ] **Step 4: Verify --init output**

```bash
./shell3 code --init
```

Expected output like:
```
Checking shell3 code dependencies...

  ✓ git (git)  (required)
  ✓ ripgrep (rg)
  ✗ gum (gum)
  ...

On macOS:
  brew install gum ...
```

- [ ] **Step 5: Run all tests**

```bash
go test ./... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/code.go cmd/shell3/main.go
git commit -m "feat: add shell3 code subcommand with --init dep check"
```

---

## Self-Review

**Spec coverage:**
- ✅ `shell3 code` — interactive REPL loop
- ✅ `shell3 code --init` — dep check + install prompt printed to stdout (no clipboard)
- ✅ Bash block extraction from model response (no tool_calls API)
- ✅ Per-turn context cancellation — ctrl+c cancels HTTP request AND subprocess
- ✅ ctx passed to `exec.CommandContext` — subprocess killed on cancel
- ✅ New ctx per turn — first ctrl+c cancels turn, second ctrl+c at prompt exits
- ✅ gum input with plain readline fallback
- ✅ System prompt with file-size check examples (wc -l before cat)
- ✅ Required deps: git. Nice deps: rg, fd, jq, gum, bat, sd, yq
- ✅ OS detection → brew (macOS) / apt (Ubuntu) install commands
- ✅ stdout only — no clipboard magic
- ✅ Model streams to terminal in real time

**No placeholders:** All code blocks complete. All commands exact.

**Type consistency:**
- `codeagent.Config.LLM` uses `LLMClient` interface → satisfied by `*llm.Client` (has `Stream` method)
- `ExtractBashBlocks` used in `runTurn` — same package, same signature
- `ExecuteBlock` used in `runTurn` — same package, same signature
- `ReadInput` used in `Run` — same package, returns `(string, error)`
