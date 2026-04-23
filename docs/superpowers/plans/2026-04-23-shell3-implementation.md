# Shell3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `shell3` — a minimal Go CLI agent that is Unix-composable, project-scoped, and extensible via shell hooks.

**Architecture:** Single Go binary with subcommands (`run`, `init`, `auth`). Core agent loop calls OpenAI-compatible LLM, dispatches tool calls sequentially, fires lifecycle hooks as shell subprocesses. Output is plain text or JSONL event stream. All project config lives in `.shell3/`, only credentials in `~/.shell3/`.

**Tech Stack:** Go 1.22+, `github.com/sashabaranov/go-openai`, `github.com/spf13/cobra`, `modernc.org/sqlite` (pure Go, no CGo), `gopkg.in/yaml.v3`

---

## Code Standards (open source — enforce strictly)

- **Godoc on every exported symbol.** Format: `// FunctionName does X.` Single line unless genuinely complex.
- **No body comments** except for non-obvious constraints (e.g. FTS5 has no native upsert, context timeout quirks). If you're tempted to write `// build tools` or `// load history`, extract a function instead.
- **Large functions = wrong.** If a function body exceeds ~40 lines, split it. `runAgent()` in the CLI wiring task must be broken up.
- **Idiomatic Go error wrapping.** Always `fmt.Errorf("package: context: %w", err)`.
- **No `//nolint` comments** — fix the underlying issue instead.
- **Package names**: single lowercase word, no underscores. `tools`, `hooks`, `memory`, `skills`, `history`, `output`, `llm`, `config`, `agent`.
- **Unexported helpers** get no godoc. Exported types/funcs/methods always do.
- **`internal/commands/`** is misleading — rename: move `InitProject` to `internal/scaffold/`, keep credential writing in `internal/config/` as `config.WriteCredentials()`.

---

## File Map

```
cmd/shell3/
  main.go                      # entry point + cobra wiring only
  run.go                       # runAgent() broken into focused helpers
  init.go                      # newInitCommand()
  auth.go                      # newAuthCommand()

internal/
  config/
    config.go                  # ProjectConfig, LoadProject()
    credentials.go             # Credentials, LoadCredentials(), WriteCredentials()
    validate.go                # Validate()

  output/
    types.go                   # Event, EventType constants
    emitter.go                 # Emitter interface, PlainEmitter, JSONLEmitter, EmitterFunc

  llm/
    client.go                  # Client, NewClient(), Stream()
    types.go                   # Message, Role, ToolDefinition, ToolCall, StreamEvent

  tools/
    tool.go                    # Tool interface
    bash.go                    # BashTool
    memory.go                  # MemorySearchTool, MemoryStoreTool

  memory/
    memory.go                  # DB, Open(), Search(), Store(), Close()

  hooks/
    hooks.go                   # Runner, all On*() methods
    types.go                   # Config, hookInput, hookOutput

  history/
    history.go                 # Load(), Save()

  skills/
    skills.go                  # Skill, LoadAll(), BuildSection()

  scaffold/
    scaffold.go                # InitProject() — creates .shell3/ structure

  agent/
    agent.go                   # Config, RunTurn()
    session.go                 # Session
```

---

## Task 1: Project Scaffold

**Files:**
- Create: `go.mod`, `go.sum`
- Create: `cmd/shell3/main.go`
- Create: `internal/config/config.go` (stub)

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/weatherjean/CODE/AGENTS/shell3
go mod init github.com/weatherjean/shell3
```

Expected: `go.mod` created with `module github.com/weatherjean/shell3` and `go 1.22`.

- [ ] **Step 2: Install dependencies**

```bash
go get github.com/sashabaranov/go-openai
go get github.com/spf13/cobra
go get gopkg.in/yaml.v3
go get modernc.org/sqlite
```

- [ ] **Step 3: Create entry point**

Create `cmd/shell3/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable coding agent",
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Verify it builds**

```bash
go build ./cmd/shell3/
./shell3 --help
```

Expected: usage text printed, no errors.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/shell3/main.go
git commit -m "feat: scaffold shell3 module and entry point"
```

---

## Task 2: Config Types

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestLoadProjectConfig(t *testing.T) {
	dir := t.TempDir()
	shell3Dir := filepath.Join(dir, ".shell3")
	os.MkdirAll(shell3Dir, 0755)

	yaml := `
model: llama3.2
provider: ollama
default_personality: coder
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ".shell3/hooks/guard.sh"
`
	os.WriteFile(filepath.Join(shell3Dir, "config.yaml"), []byte(yaml), 0644)

	cfg, err := config.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "llama3.2" {
		t.Errorf("got model %q, want llama3.2", cfg.Model)
	}
	if cfg.Hooks.OnToolCall != ".shell3/hooks/guard.sh" {
		t.Errorf("got hook %q", cfg.Hooks.OnToolCall)
	}
}

func TestLoadProjectConfig_Missing(t *testing.T) {
	_, err := config.LoadProject(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -v
```

Expected: FAIL — `config` package does not exist.

- [ ] **Step 3: Implement config types**

Create `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Hooks struct {
	OnSessionStart  string `yaml:"on_session_start"`
	OnSessionEnd    string `yaml:"on_session_end"`
	OnTurnStart     string `yaml:"on_turn_start"`
	OnTurnEnd       string `yaml:"on_turn_end"`
	OnToolCall      string `yaml:"on_tool_call"`
	OnToolResult    string `yaml:"on_tool_result"`
	OnContextBuild  string `yaml:"on_context_build"`
	OnError         string `yaml:"on_error"`
}

type ProjectConfig struct {
	Model              string `yaml:"model"`
	Provider           string `yaml:"provider"`
	DefaultPersonality string `yaml:"default_personality"`
	MemoryDB           string `yaml:"memory_db"`
	HistoryMD          string `yaml:"history_md"`
	Hooks              Hooks  `yaml:"hooks"`
}

func LoadProject(projectDir string) (*ProjectConfig, error) {
	path := filepath.Join(projectDir, ".shell3", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no .shell3/config.yaml found — run: shell3 init")
		}
		return nil, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add ProjectConfig with YAML loading"
```

---

## Task 3: Credentials

**Files:**
- Create: `internal/config/credentials.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0755)

	yaml := `
providers:
  ollama:
    base_url: http://localhost:11434/v1
  openai:
    api_key: sk-test123
    base_url: https://api.openai.com/v1
`
	os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(yaml), 0644)

	creds, err := config.LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := creds.Providers["openai"]
	if !ok {
		t.Fatal("expected openai provider")
	}
	if p.APIKey != "sk-test123" {
		t.Errorf("got api_key %q", p.APIKey)
	}
}

func TestLoadCredentials_Missing(t *testing.T) {
	_, err := config.LoadCredentials(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -v -run TestLoadCredentials
```

Expected: FAIL — `LoadCredentials` not defined.

- [ ] **Step 3: Implement credentials**

Create `internal/config/credentials.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ProviderCredentials struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

type Credentials struct {
	Providers map[string]ProviderCredentials `yaml:"providers"`
}

// LoadCredentials reads ~/.shell3/credentials.yaml.
// Pass os.UserHomeDir() result as homeDir.
func LoadCredentials(homeDir string) (*Credentials, error) {
	path := filepath.Join(homeDir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no credentials found — run: shell3 auth")
		}
		return nil, err
	}
	var creds Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("invalid credentials file: %w", err)
	}
	return &creds, nil
}

func (c *Credentials) Get(provider string) (ProviderCredentials, error) {
	p, ok := c.Providers[provider]
	if !ok {
		return ProviderCredentials{}, fmt.Errorf("no credentials for provider %q — run: shell3 auth", provider)
	}
	return p, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/credentials.go internal/config/config_test.go
git commit -m "feat: add Credentials loading from ~/.shell3/credentials.yaml"
```

---

## Task 4: Startup Validation

**Files:**
- Create: `internal/config/validate.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestValidate_OK(t *testing.T) {
	cfg := &config.ProjectConfig{Model: "llama3.2", Provider: "ollama"}
	creds := &config.Credentials{
		Providers: map[string]config.ProviderCredentials{
			"ollama": {BaseURL: "http://localhost:11434/v1"},
		},
	}
	if err := config.Validate(cfg, creds); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingProvider(t *testing.T) {
	cfg := &config.ProjectConfig{Model: "llama3.2", Provider: "openai"}
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{}}
	if err := config.Validate(cfg, creds); err == nil {
		t.Error("expected error for missing provider credentials")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -v -run TestValidate
```

Expected: FAIL — `Validate` not defined.

- [ ] **Step 3: Implement validation**

Create `internal/config/validate.go`:

```go
package config

import "fmt"

func Validate(cfg *ProjectConfig, creds *Credentials) error {
	if cfg.Model == "" {
		return fmt.Errorf("config: model is required")
	}
	if cfg.Provider == "" {
		return fmt.Errorf("config: provider is required")
	}
	if _, err := creds.Get(cfg.Provider); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/validate.go internal/config/config_test.go
git commit -m "feat: add startup config + credential validation"
```

---

## Task 5: Event Types and Output Emitter

**Files:**
- Create: `internal/output/types.go`
- Create: `internal/output/emitter.go`
- Create: `internal/output/emitter_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/output/emitter_test.go`:

```go
package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/output"
)

func TestPlainEmitter(t *testing.T) {
	var buf bytes.Buffer
	e := output.NewPlainEmitter(&buf)
	e.Emit(output.Event{Type: output.EventToken, Text: "hello"})
	e.Emit(output.Event{Type: output.EventToken, Text: " world"})
	e.Emit(output.Event{Type: output.EventDone, Text: "hello world"})
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected output to contain 'hello', got: %q", buf.String())
	}
}

func TestJSONLEmitter(t *testing.T) {
	var buf bytes.Buffer
	e := output.NewJSONLEmitter(&buf)
	e.Emit(output.Event{Type: output.EventToken, Text: "hi"})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var ev output.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != output.EventToken || ev.Text != "hi" {
		t.Errorf("unexpected event: %+v", ev)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/output/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Create event types**

Create `internal/output/types.go`:

```go
package output

type EventType string

const (
	EventThinking   EventType = "thinking"
	EventToken      EventType = "token"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)

type Event struct {
	Type       EventType      `json:"type"`
	Text       string         `json:"text,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	ExitCode   int            `json:"exit_code,omitempty"`
	Message    string         `json:"message,omitempty"`
}
```

- [ ] **Step 4: Create emitters**

Create `internal/output/emitter.go`:

```go
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

type Emitter interface {
	Emit(Event)
}

type PlainEmitter struct{ w io.Writer }

func NewPlainEmitter(w io.Writer) *PlainEmitter { return &PlainEmitter{w} }

func (e *PlainEmitter) Emit(ev Event) {
	switch ev.Type {
	case EventThinking:
		fmt.Fprintf(e.w, "thinking: %s\n", ev.Text)
	case EventToken:
		fmt.Fprint(e.w, ev.Text)
	case EventToolCall:
		fmt.Fprintf(e.w, "\n[%s] %v\n", ev.Tool, ev.Params)
	case EventToolResult:
		fmt.Fprintf(e.w, "%s\n[%s done]\n", ev.Text, ev.Tool)
	case EventDone:
		fmt.Fprintln(e.w)
	case EventError:
		fmt.Fprintf(e.w, "error: %s\n", ev.Message)
	}
}

type JSONLEmitter struct{ w io.Writer }

func NewJSONLEmitter(w io.Writer) *JSONLEmitter { return &JSONLEmitter{w} }

func (e *JSONLEmitter) Emit(ev Event) {
	b, _ := json.Marshal(ev)
	fmt.Fprintf(e.w, "%s\n", b)
}

// OutEmitter spawns CMD and pipes events to its stdin.
type OutEmitter struct {
	inner   Emitter
	cmdArgs []string
}

func NewOutEmitter(inner Emitter, cmdArgs []string) *OutEmitter {
	return &OutEmitter{inner: inner, cmdArgs: cmdArgs}
}

func (e *OutEmitter) Emit(ev Event) {
	e.inner.Emit(ev)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/output/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/output/
git commit -m "feat: add Event types and Plain/JSONL emitters"
```

---

## Task 6: LLM Client

**Files:**
- Create: `internal/llm/types.go`
- Create: `internal/llm/client.go`
- Create: `internal/llm/client_test.go`

- [ ] **Step 1: Create LLM types**

Create `internal/llm/types.go`:

```go
package llm

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role   `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string
	Name     string
	RawArgs  string
}

type StreamEvent struct {
	TextDelta string
	ToolCall  *ToolCall
	Done      bool
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/llm/client_test.go`:

```go
package llm_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestNewClient_Smoke(t *testing.T) {
	c := llm.NewClient("http://localhost:11434/v1", "", "llama3.2")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/llm/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement LLM client**

Create `internal/llm/client.go`:

```go
package llm

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	oc    *openai.Client
	model string
}

func NewClient(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Client{
		oc:    openai.NewClientWithConfig(cfg),
		model: model,
	}
}

func (c *Client) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error {
	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: toOpenAI(msgs),
		Stream:   true,
	}
	if len(tools) > 0 {
		req.Tools = toOpenAITools(tools)
	}

	stream, err := c.oc.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return fmt.Errorf("llm stream: %w", err)
	}
	defer stream.Close()

	// accumulate tool calls across chunks
	toolCalls := map[int]*ToolCall{}

	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			onEvent(StreamEvent{TextDelta: delta.Content})
		}

		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if idx == nil {
				continue
			}
			if toolCalls[*idx] == nil {
				toolCalls[*idx] = &ToolCall{ID: tc.ID, Name: tc.Function.Name}
			}
			toolCalls[*idx].RawArgs += tc.Function.Arguments
		}

		if chunk.Choices[0].FinishReason == "tool_calls" {
			for i := 0; i < len(toolCalls); i++ {
				onEvent(StreamEvent{ToolCall: toolCalls[i]})
			}
		}
	}

	onEvent(StreamEvent{Done: true})
	return nil
}

func toOpenAI(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = openai.ChatCompletionMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
	}
	return out
}

func toOpenAITools(tools []ToolDefinition) []openai.Tool {
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return out
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/llm/... -v
```

Expected: PASS (smoke test only — no live LLM call).

- [ ] **Step 6: Commit**

```bash
git add internal/llm/
git commit -m "feat: add OpenAI-compatible LLM streaming client"
```

---

## Task 7: Tool Interface and Bash Tool

**Files:**
- Create: `internal/tools/tool.go`
- Create: `internal/tools/bash.go`
- Create: `internal/tools/bash_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tools/bash_test.go`:

```go
package tools_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/tools"
)

func TestBashTool_Echo(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 10)
	result, err := bash.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello\n" {
		t.Errorf("got %q, want %q", result, "hello\n")
	}
}

func TestBashTool_ExitCode(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 10)
	_, err := bash.Execute(context.Background(), map[string]any{"command": "exit 1"})
	if err == nil {
		t.Error("expected error for non-zero exit")
	}
}

func TestBashTool_Timeout(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 1) // 1 second timeout
	_, err := bash.Execute(context.Background(), map[string]any{"command": "sleep 5"})
	if err == nil {
		t.Error("expected timeout error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/tools/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Create tool interface**

Create `internal/tools/tool.go`:

```go
package tools

import (
	"context"

	"github.com/weatherjean/shell3/internal/llm"
)

type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, params map[string]any) (string, error)
}
```

- [ ] **Step 4: Implement bash tool**

Create `internal/tools/bash.go`:

```go
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

type BashTool struct {
	cwd        string
	timeoutSec int
}

func NewBashTool(cwd string, timeoutSec int) *BashTool {
	return &BashTool{cwd: cwd, timeoutSec: timeoutSec}
}

func (t *BashTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "bash",
		Description: "Execute a shell command in the project directory. Use for reading files, running tests, making changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	cmd, ok := params["command"].(string)
	if !ok || cmd == "" {
		return "", fmt.Errorf("bash: command param required")
	}

	timeout := time.Duration(t.timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = t.cwd

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		out := stdout.String() + stderr.String()
		return out, fmt.Errorf("bash exit error: %w\n%s", err, out)
	}
	return stdout.String() + stderr.String(), nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tools/... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tools/
git commit -m "feat: add Tool interface and bash tool with timeout"
```

---

## Task 8: SQLite Memory

**Files:**
- Create: `internal/memory/memory.go`
- Create: `internal/memory/memory_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/memory/memory_test.go`:

```go
package memory_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/memory"
)

func TestMemory_StoreAndSearch(t *testing.T) {
	db, err := memory.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Store("auth-decision", "use JWT with 1h expiry"); err != nil {
		t.Fatal(err)
	}
	if err := db.Store("deploy-notes", "always run migrations before deploy"); err != nil {
		t.Fatal(err)
	}

	results, err := db.Search("JWT")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Key != "auth-decision" {
		t.Errorf("got key %q, want auth-decision", results[0].Key)
	}
}

func TestMemory_Upsert(t *testing.T) {
	db, err := memory.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Store("key1", "original value")
	db.Store("key1", "updated value")

	results, err := db.Search("updated")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/memory/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement SQLite memory**

Create `internal/memory/memory.go`:

```go
package memory

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Entry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open %s: %w", path, err)
	}
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memories USING fts5(
			key,
			value,
			updated_at UNINDEXED
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("memory: create table: %w", err)
	}
	return &DB{sql: db}, nil
}

func (d *DB) Store(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	// delete existing then insert (FTS5 has no native upsert)
	_, err := d.sql.Exec(`DELETE FROM memories WHERE key = ?`, key)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`INSERT INTO memories(key, value, updated_at) VALUES(?, ?, ?)`, key, value, now)
	return err
}

func (d *DB) Search(query string) ([]Entry, error) {
	rows, err := d.sql.Query(`
		SELECT key, value, updated_at
		FROM memories
		WHERE memories MATCH ?
		ORDER BY rank
		LIMIT 5
	`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Entry
	for rows.Next() {
		var e Entry
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &updatedAt); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}

func (d *DB) Close() error { return d.sql.Close() }
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/memory/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat: add SQLite FTS5 memory store"
```

---

## Task 9: Memory Tools

**Files:**
- Create: `internal/tools/memory.go`
- Modify: `internal/tools/bash_test.go` → create `internal/tools/memory_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tools/memory_test.go`:

```go
package tools_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/memory"
	"github.com/weatherjean/shell3/internal/tools"
)

func TestMemorySearchTool(t *testing.T) {
	db, _ := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()
	db.Store("jwt", "use JWT with 1h expiry")

	tool := tools.NewMemorySearchTool(db)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMemoryStoreTool(t *testing.T) {
	db, _ := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()

	tool := tools.NewMemoryStoreTool(db)
	_, err := tool.Execute(context.Background(), map[string]any{
		"key":   "auth",
		"value": "JWT tokens",
	})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := db.Search("JWT")
	if len(results) == 0 {
		t.Error("expected stored entry to be searchable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/tools/... -v -run TestMemory
```

Expected: FAIL — `NewMemorySearchTool` not defined.

- [ ] **Step 3: Implement memory tools**

Create `internal/tools/memory.go`:

```go
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/memory"
)

type MemorySearchTool struct{ db *memory.DB }

func NewMemorySearchTool(db *memory.DB) *MemorySearchTool { return &MemorySearchTool{db} }

func (t *MemorySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_search",
		Description: "Search project memory for relevant past decisions, notes, or context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.Search(q)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No memories found.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
	}
	return sb.String(), nil
}

type MemoryStoreTool struct{ db *memory.DB }

func NewMemoryStoreTool(db *memory.DB) *MemoryStoreTool { return &MemoryStoreTool{db} }

func (t *MemoryStoreTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_store",
		Description: "Store a key-value entry in project memory for future reference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember"},
			},
			"required": []string{"key", "value"},
		},
	}
}

func (t *MemoryStoreTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	value, _ := params["value"].(string)
	if err := t.db.Store(key, value); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stored: %s", key), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tools/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/memory.go internal/tools/memory_test.go
git commit -m "feat: add memory_search and memory_store tools"
```

---

## Task 10: Hooks Runner

**Files:**
- Create: `internal/hooks/types.go`
- Create: `internal/hooks/hooks.go`
- Create: `internal/hooks/hooks_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/hooks/hooks_test.go`:

```go
package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestHookAllow(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
echo '{"action":"allow"}'`), 0755)

	r := hooks.NewRunner(hooks.Config{OnToolCall: script})
	allowed, err := r.OnToolCall(context.Background(), "bash", map[string]any{"command": "ls"})
	if err != nil || !allowed {
		t.Errorf("expected allow, got allowed=%v err=%v", allowed, err)
	}
}

func TestHookBlock(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
echo '{"action":"block","reason":"not allowed"}'`), 0755)

	r := hooks.NewRunner(hooks.Config{OnToolCall: script})
	allowed, err := r.OnToolCall(context.Background(), "bash", map[string]any{"command": "rm -rf /"})
	if err == nil || allowed {
		t.Errorf("expected block, got allowed=%v err=%v", allowed, err)
	}
}

func TestContextBuildHook(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
cat | python3 -c "import sys,json; d=json.load(sys.stdin); d['messages']=d['messages'][-1:]; print(json.dumps(d))"
`), 0755)

	r := hooks.NewRunner(hooks.Config{OnContextBuild: script})
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleUser, Content: "second"},
	}
	out, err := r.OnContextBuild(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Content != "second" {
		t.Errorf("expected 1 message 'second', got %+v", out)
	}
}

func TestNoHook(t *testing.T) {
	r := hooks.NewRunner(hooks.Config{})
	allowed, err := r.OnToolCall(context.Background(), "bash", nil)
	if err != nil || !allowed {
		t.Errorf("no hook should default to allow: allowed=%v err=%v", allowed, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/hooks/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Create hook types**

Create `internal/hooks/types.go`:

```go
package hooks

type Config struct {
	OnSessionStart string `yaml:"on_session_start"`
	OnSessionEnd   string `yaml:"on_session_end"`
	OnTurnStart    string `yaml:"on_turn_start"`
	OnTurnEnd      string `yaml:"on_turn_end"`
	OnToolCall     string `yaml:"on_tool_call"`
	OnToolResult   string `yaml:"on_tool_result"`
	OnContextBuild string `yaml:"on_context_build"`
	OnError        string `yaml:"on_error"`
}

type hookInput struct {
	Hook    string         `json:"hook"`
	Tool    string         `json:"tool,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Messages any           `json:"messages,omitempty"`
}

type hookOutput struct {
	Action   string         `json:"action"`
	Reason   string         `json:"reason,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Messages any            `json:"messages,omitempty"`
}
```

- [ ] **Step 4: Implement hooks runner**

Create `internal/hooks/hooks.go`:

```go
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

const hookTimeoutSec = 5

type Runner struct{ cfg Config }

func NewRunner(cfg Config) *Runner { return &Runner{cfg} }

func (r *Runner) callHook(ctx context.Context, cmd string, input hookInput) (hookOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, hookTimeoutSec*time.Second)
	defer cancel()

	data, _ := json.Marshal(input)
	parts := strings.Fields(cmd)
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Stdin = bytes.NewReader(data)

	var stdout bytes.Buffer
	c.Stdout = &stdout

	if err := c.Run(); err != nil {
		return hookOutput{}, fmt.Errorf("hook %q failed: %w", cmd, err)
	}

	if stdout.Len() == 0 {
		return hookOutput{Action: "allow"}, nil
	}

	var out hookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return hookOutput{}, fmt.Errorf("hook %q bad JSON output: %w", cmd, err)
	}
	return out, nil
}

// OnToolCall returns true if tool call is allowed.
func (r *Runner) OnToolCall(ctx context.Context, tool string, params map[string]any) (bool, error) {
	if r.cfg.OnToolCall == "" {
		return true, nil
	}
	out, err := r.callHook(ctx, r.cfg.OnToolCall, hookInput{
		Hook: "on_tool_call", Tool: tool, Params: params,
	})
	if err != nil {
		return false, err
	}
	if out.Action == "block" {
		return false, fmt.Errorf("hook blocked tool call: %s", out.Reason)
	}
	return true, nil
}

// OnContextBuild transforms messages before LLM call.
func (r *Runner) OnContextBuild(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	if r.cfg.OnContextBuild == "" {
		return msgs, nil
	}
	out, err := r.callHook(ctx, r.cfg.OnContextBuild, hookInput{
		Hook: "on_context_build", Messages: msgs,
	})
	if err != nil {
		return msgs, err
	}
	if out.Messages == nil {
		return msgs, nil
	}
	b, _ := json.Marshal(out.Messages)
	var result []llm.Message
	if err := json.Unmarshal(b, &result); err != nil {
		return msgs, fmt.Errorf("hook on_context_build: bad messages JSON: %w", err)
	}
	return result, nil
}

// Informational hooks — fire and forget (log on error, don't stop).
func (r *Runner) OnSessionStart(ctx context.Context) {
	if r.cfg.OnSessionStart != "" {
		r.callHook(ctx, r.cfg.OnSessionStart, hookInput{Hook: "on_session_start"}) //nolint
	}
}

func (r *Runner) OnSessionEnd(ctx context.Context) {
	if r.cfg.OnSessionEnd != "" {
		r.callHook(ctx, r.cfg.OnSessionEnd, hookInput{Hook: "on_session_end"}) //nolint
	}
}

func (r *Runner) OnTurnStart(ctx context.Context) {
	if r.cfg.OnTurnStart != "" {
		r.callHook(ctx, r.cfg.OnTurnStart, hookInput{Hook: "on_turn_start"}) //nolint
	}
}

func (r *Runner) OnTurnEnd(ctx context.Context, response string) {
	if r.cfg.OnTurnEnd != "" {
		r.callHook(ctx, r.cfg.OnTurnEnd, hookInput{Hook: "on_turn_end", Params: map[string]any{"response": response}}) //nolint
	}
}

func (r *Runner) OnToolResult(ctx context.Context, tool, result string) {
	if r.cfg.OnToolResult != "" {
		r.callHook(ctx, r.cfg.OnToolResult, hookInput{Hook: "on_tool_result", Tool: tool, Params: map[string]any{"result": result}}) //nolint
	}
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/hooks/... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/hooks/
git commit -m "feat: add hooks runner with allow/block/context-build support"
```

---

## Task 11: Skills Loader

**Files:**
- Create: `internal/skills/skills.go`
- Create: `internal/skills/skills_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/skills/skills_test.go`:

```go
package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/skills"
)

func TestLoadSkills(t *testing.T) {
	dir := t.TempDir()
	content := "---\nname: git-workflow\ndescription: Git conventions\n---\nAlways squash before merging."
	os.WriteFile(filepath.Join(dir, "git-workflow.md"), []byte(content), 0644)

	loaded, err := skills.LoadAll([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Name != "git-workflow" {
		t.Errorf("got name %q", loaded[0].Name)
	}
	if !strings.Contains(loaded[0].Body, "squash") {
		t.Errorf("expected body to contain content")
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	s := []skills.Skill{{Name: "git", Description: "git stuff", Body: "always squash"}}
	prompt := skills.BuildSection(s)
	if !strings.Contains(prompt, "# Skills") {
		t.Error("expected # Skills header")
	}
	if !strings.Contains(prompt, "always squash") {
		t.Error("expected skill body in prompt")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/skills/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement skills loader**

Create `internal/skills/skills.go`:

```go
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	Name        string
	Description string
	Body        string
}

// LoadAll loads all .md files from given directories.
func LoadAll(dirs []string) ([]Skill, error) {
	var result []Skill
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			s, err := parse(string(data))
			if err != nil {
				return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
			}
			result = append(result, s)
		}
	}
	return result, nil
}

func parse(content string) (Skill, error) {
	if !strings.HasPrefix(content, "---") {
		return Skill{Body: strings.TrimSpace(content)}, nil
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return Skill{Body: strings.TrimSpace(content)}, nil
	}
	fm := parts[1]
	body := strings.TrimSpace(parts[2])

	s := Skill{Body: body}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if kv := strings.SplitN(line, ":", 2); len(kv) == 2 {
			k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
			switch k {
			case "name":
				s.Name = v
			case "description":
				s.Description = v
			}
		}
	}
	return s, nil
}

// BuildSection formats skills into the system prompt section.
func BuildSection(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n# Skills\n\n")
	for _, s := range skills {
		if s.Name != "" {
			fmt.Fprintf(&sb, "## %s\n", s.Name)
			if s.Description != "" {
				fmt.Fprintf(&sb, "%s\n\n", s.Description)
			}
		}
		sb.WriteString(s.Body)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/skills/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/skills/
git commit -m "feat: add skills loader and system prompt section builder"
```

---

## Task 12: Session History (Markdown)

**Files:**
- Create: `internal/history/history.go`
- Create: `internal/history/history_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/history/history_test.go`:

```go
package history_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/history"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.md")

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "world"},
	}

	if err := history.Save(path, msgs); err != nil {
		t.Fatal(err)
	}

	loaded, err := history.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Content != "hello" || loaded[1].Content != "world" {
		t.Errorf("unexpected messages: %+v", loaded)
	}
}

func TestLoad_Missing(t *testing.T) {
	msgs, err := history.Load("/nonexistent/path.md")
	if err != nil {
		t.Fatal("missing file should return empty, not error")
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d messages", len(msgs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/history/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement history**

Create `internal/history/history.go`:

```go
package history

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Load reads a markdown history file. Returns empty slice if file doesn't exist.
func Load(path string) ([]llm.Message, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(string(data)), nil
}

// Save writes messages to markdown file, overwriting any existing content.
func Save(path string, msgs []llm.Message) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Session: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	for _, m := range msgs {
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", roleLabel(m.Role), m.Content)
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func parse(content string) []llm.Message {
	var msgs []llm.Message
	sections := strings.Split(content, "\n## ")
	for _, sec := range sections[1:] { // skip header
		lines := strings.SplitN(sec, "\n", 3)
		if len(lines) < 3 {
			continue
		}
		roleStr := strings.TrimSpace(lines[0])
		body := strings.TrimSpace(lines[2])
		role := labelToRole(roleStr)
		if role == "" {
			continue
		}
		msgs = append(msgs, llm.Message{Role: role, Content: body})
	}
	return msgs
}

func roleLabel(r llm.Role) string {
	switch r {
	case llm.RoleUser:
		return "User"
	case llm.RoleAssistant:
		return "Assistant"
	default:
		return string(r)
	}
}

func labelToRole(s string) llm.Role {
	switch strings.ToLower(s) {
	case "user":
		return llm.RoleUser
	case "assistant":
		return llm.RoleAssistant
	}
	return ""
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/history/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/history/
git commit -m "feat: add markdown session history load/save"
```

---

## Task 13: Core Agent Loop

**Files:**
- Create: `internal/agent/session.go`
- Create: `internal/agent/agent.go`
- Create: `internal/agent/agent_test.go`

- [ ] **Step 1: Create session type**

Create `internal/agent/session.go`:

```go
package agent

import "github.com/weatherjean/shell3/internal/llm"

type Session struct {
	Messages []llm.Message
}

func (s *Session) Append(m llm.Message) {
	s.Messages = append(s.Messages, m)
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/agent/agent_test.go`:

```go
package agent_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/agent"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/output"
)

type fakeClient struct{ responses []string }

func (f *fakeClient) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	for _, r := range f.responses {
		onEvent(llm.StreamEvent{TextDelta: r})
	}
	onEvent(llm.StreamEvent{Done: true})
	return nil
}

func TestAgentRun_SimpleResponse(t *testing.T) {
	client := &fakeClient{responses: []string{"hello ", "world"}}
	var events []output.Event
	emit := output.EmitterFunc(func(e output.Event) { events = append(events, e) })

	cfg := agent.Config{
		SystemPrompt: "you are helpful",
		LLM:          client,
		Emitter:      emit,
	}
	sess := &agent.Session{}
	if err := agent.RunTurn(context.Background(), cfg, sess, "hi"); err != nil {
		t.Fatal(err)
	}

	var done *output.Event
	for i := range events {
		if events[i].Type == output.EventDone {
			done = &events[i]
		}
	}
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Text != "hello world" {
		t.Errorf("got %q", done.Text)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/agent/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 4: Add EmitterFunc to output package**

Append to `internal/output/emitter.go`:

```go
// EmitterFunc is a func that implements Emitter.
type EmitterFunc func(Event)

func (f EmitterFunc) Emit(e Event) { f(e) }
```

- [ ] **Step 5: Implement core agent loop**

Create `internal/agent/agent.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/output"
	"github.com/weatherjean/shell3/internal/tools"
)

type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

type Config struct {
	SystemPrompt string
	LLM          LLMClient
	Tools        []tools.Tool
	Hooks        *hooks.Runner
	Emitter      output.Emitter
}

// RunTurn executes one user→assistant turn, appending to session.
func RunTurn(ctx context.Context, cfg Config, sess *Session, userInput string) error {
	if cfg.Hooks == nil {
		cfg.Hooks = hooks.NewRunner(hooks.Config{})
	}

	sess.Append(llm.Message{Role: llm.RoleUser, Content: userInput})

	msgs, err := cfg.Hooks.OnContextBuild(ctx, sess.Messages)
	if err != nil {
		msgs = sess.Messages
	}

	// prepend system prompt
	allMsgs := append([]llm.Message{{Role: llm.RoleSystem, Content: cfg.SystemPrompt}}, msgs...)

	defs := make([]llm.ToolDefinition, len(cfg.Tools))
	for i, t := range cfg.Tools {
		defs[i] = t.Definition()
	}

	var responseText strings.Builder
	var pendingToolCalls []llm.ToolCall

	err = cfg.LLM.Stream(ctx, allMsgs, defs, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			responseText.WriteString(ev.TextDelta)
			cfg.Emitter.Emit(output.Event{Type: output.EventToken, Text: ev.TextDelta})
		}
		if ev.ToolCall != nil {
			pendingToolCalls = append(pendingToolCalls, *ev.ToolCall)
		}
	})
	if err != nil {
		cfg.Emitter.Emit(output.Event{Type: output.EventError, Message: err.Error()})
		return err
	}

	// Execute tool calls sequentially
	for _, tc := range pendingToolCalls {
		cfg.Emitter.Emit(output.Event{Type: output.EventToolCall, Tool: tc.Name})

		var params map[string]any
		json.Unmarshal([]byte(tc.RawArgs), &params)

		allowed, err := cfg.Hooks.OnToolCall(ctx, tc.Name, params)
		if err != nil || !allowed {
			result := fmt.Sprintf("Tool call blocked: %v", err)
			sess.Append(llm.Message{Role: llm.RoleTool, Content: result, ToolCallID: tc.ID, Name: tc.Name})
			cfg.Emitter.Emit(output.Event{Type: output.EventToolResult, Tool: tc.Name, Text: result})
			continue
		}

		result, toolErr := executeTool(ctx, cfg.Tools, tc.Name, params)
		if toolErr != nil {
			result = fmt.Sprintf("error: %v", toolErr)
		}

		cfg.Hooks.OnToolResult(ctx, tc.Name, result)
		sess.Append(llm.Message{Role: llm.RoleTool, Content: result, ToolCallID: tc.ID, Name: tc.Name})
		cfg.Emitter.Emit(output.Event{Type: output.EventToolResult, Tool: tc.Name, Text: result})
	}

	fullText := responseText.String()
	sess.Append(llm.Message{Role: llm.RoleAssistant, Content: fullText})
	cfg.Emitter.Emit(output.Event{Type: output.EventDone, Text: fullText})
	cfg.Hooks.OnTurnEnd(ctx, fullText)

	return nil
}

func executeTool(ctx context.Context, ts []tools.Tool, name string, params map[string]any) (string, error) {
	for _, t := range ts {
		if t.Definition().Name == name {
			return t.Execute(ctx, params)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/... -v
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/ internal/output/emitter.go
git commit -m "feat: add core agent loop with tool dispatch and hook integration"
```

---

## Task 14: `shell3 init` Command

**Files:**
- Create: `internal/commands/init.go`

- [ ] **Step 1: Write the failing test**

Create `internal/commands/init_test.go`:

```go
package commands_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/commands"
)

func TestInit_CreatesShell3Dir(t *testing.T) {
	dir := t.TempDir()
	if err := commands.InitProject(dir); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(dir, ".shell3", "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected .shell3/config.yaml to exist: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".shell3", ".gitignore")
	if _, err := os.Stat(gitignorePath); err != nil {
		t.Errorf("expected .shell3/.gitignore to exist: %v", err)
	}
}

func TestInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	commands.InitProject(dir)
	// second init should not error
	if err := commands.InitProject(dir); err != nil {
		t.Errorf("re-init should be safe: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/commands/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement init command**

Create `internal/commands/init.go`:

```go
package commands

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

func InitProject(projectDir string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	personalitiesDir := filepath.Join(shell3Dir, "personalities")
	skillsDir := filepath.Join(shell3Dir, "skills")
	hooksDir := filepath.Join(shell3Dir, "hooks")

	for _, d := range []string{shell3Dir, personalitiesDir, skillsDir, hooksDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("init: mkdir %s: %w", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(shell3Dir, "config.yaml"):                        defaultConfig,
		filepath.Join(shell3Dir, ".gitignore"):                         defaultGitignore,
		filepath.Join(personalitiesDir, "coder.yaml"):                  defaultCoderPersonality,
	}

	for path, content := range files {
		if _, err := os.Stat(path); err == nil {
			continue // don't overwrite existing
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("init: write %s: %w", path, err)
		}
	}

	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	fmt.Println("Next: run `shell3 auth` to configure your LLM credentials.")
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/commands/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/init.go internal/commands/init_test.go
git commit -m "feat: add shell3 init command with default scaffolding"
```

---

## Task 15: `shell3 auth` Command

**Files:**
- Create: `internal/commands/auth.go`
- Create: `internal/commands/auth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/commands/auth_test.go`:

```go
package commands_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/commands"
)

func TestWriteCredentials(t *testing.T) {
	dir := t.TempDir()
	err := commands.WriteCredentials(dir, "ollama", "", "http://localhost:11434/v1")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Error("expected non-empty credentials file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/commands/... -v -run TestWriteCredentials
```

Expected: FAIL — `WriteCredentials` not defined.

- [ ] **Step 3: Implement auth**

Create `internal/commands/auth.go`:

```go
package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"github.com/weatherjean/shell3/internal/config"
)

func WriteCredentials(homeDir, provider, apiKey, baseURL string) error {
	shell3Dir := filepath.Join(homeDir, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0700); err != nil {
		return err
	}

	path := filepath.Join(shell3Dir, "credentials.yaml")

	// load existing or start fresh
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{}}
	if data, err := os.ReadFile(path); err == nil {
		yaml.Unmarshal(data, creds)
	}
	if creds.Providers == nil {
		creds.Providers = map[string]config.ProviderCredentials{}
	}

	creds.Providers[provider] = config.ProviderCredentials{APIKey: apiKey, BaseURL: baseURL}

	data, err := yaml.Marshal(creds)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("auth: write credentials: %w", err)
	}
	fmt.Printf("Credentials for %q saved to %s\n", provider, path)
	return nil
}

// RunAuthInteractive prompts the user for provider credentials on stdin.
func RunAuthInteractive(homeDir string) error {
	var provider, apiKey, baseURL string

	fmt.Print("Provider (ollama/openai/z_ai/codex_plus): ")
	fmt.Scan(&provider)

	fmt.Print("Base URL (leave empty for provider default): ")
	fmt.Scan(&baseURL)

	fmt.Print("API Key (leave empty if not required): ")
	fmt.Scan(&apiKey)

	return WriteCredentials(homeDir, provider, apiKey, baseURL)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/commands/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/auth.go internal/commands/auth_test.go
git commit -m "feat: add shell3 auth command for credential setup"
```

---

## Task 16: CLI Wiring

**Files:**
- Modify: `cmd/shell3/main.go`
- Create: `cmd/shell3/run.go`

- [ ] **Step 1: Create run command**

Create `cmd/shell3/run.go`:

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/agent"
	"github.com/weatherjean/shell3/internal/commands"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/history"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/memory"
	"github.com/weatherjean/shell3/internal/output"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/tools"
)

type runFlags struct {
	personality string
	configPath  string
	model       string
	baseURL     string
	apiKey      string
	memoryDB    string
	historyMD   string
	stream      bool
	out         string
	skillPaths  []string
	noBash      bool
	noMemory    bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	cmd.Flags().StringVar(&f.personality, "personality", "", "Named personality")
	cmd.Flags().StringVar(&f.configPath, "config", "", "Personality YAML file path")
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().StringVar(&f.memoryDB, "memory-db", "", "SQLite memory DB path")
	cmd.Flags().StringVar(&f.historyMD, "history-md", "", "Markdown history file path")
	cmd.Flags().BoolVar(&f.stream, "stream", false, "Emit JSONL event stream")
	cmd.Flags().StringVar(&f.out, "out", "", "Pipe output to this command")
	cmd.Flags().StringSliceVar(&f.skillPaths, "skills", nil, "Additional skill directories")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory tools")
	return cmd
}

func runAgent(ctx context.Context, f *runFlags, initialInput string) error {
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

	// flags override config
	model := projCfg.Model
	if f.model != "" {
		model = f.model
	}
	provider := projCfg.Provider
	provCreds, _ := creds.Get(provider)
	baseURL := provCreds.BaseURL
	if f.baseURL != "" {
		baseURL = f.baseURL
	}
	apiKey := provCreds.APIKey
	if f.apiKey != "" {
		apiKey = f.apiKey
	}
	memoryDB := projCfg.MemoryDB
	if f.memoryDB != "" {
		memoryDB = f.memoryDB
	}
	historyMD := projCfg.HistoryMD
	if f.historyMD != "" {
		historyMD = f.historyMD
	}

	// build emitter
	var emitter output.Emitter
	if f.stream {
		emitter = output.NewJSONLEmitter(os.Stdout)
	} else {
		emitter = output.NewPlainEmitter(os.Stdout)
	}

	// build tools
	var ts []tools.Tool
	if !f.noBash {
		ts = append(ts, tools.NewBashTool(cwd, 30))
	}
	var memDB *memory.DB
	if !f.noMemory && memoryDB != "" {
		memDB, err = memory.Open(memoryDB)
		if err != nil {
			return fmt.Errorf("memory: %w", err)
		}
		defer memDB.Close()
		ts = append(ts, tools.NewMemorySearchTool(memDB))
		ts = append(ts, tools.NewMemoryStoreTool(memDB))
	}

	// load skills
	skillDirs := []string{".shell3/skills"}
	skillDirs = append(skillDirs, f.skillPaths...)
	loadedSkills, _ := skills.LoadAll(skillDirs)
	systemPrompt := "You are an expert software engineer. Use tools to accomplish tasks.\n"
	systemPrompt += skills.BuildSection(loadedSkills)

	// load history
	sess := &agent.Session{}
	if historyMD != "" {
		msgs, err := history.Load(historyMD)
		if err != nil {
			return err
		}
		sess.Messages = msgs
	}

	// hooks
	hookRunner := hooks.NewRunner(hooks.Config(projCfg.Hooks))

	agentCfg := agent.Config{
		SystemPrompt: systemPrompt,
		LLM:          llm.NewClient(baseURL, apiKey, model),
		Tools:        ts,
		Hooks:        hookRunner,
		Emitter:      emitter,
	}

	hookRunner.OnSessionStart(ctx)
	defer hookRunner.OnSessionEnd(ctx)

	if initialInput != "" {
		return runAndSave(ctx, agentCfg, sess, initialInput, historyMD)
	}

	// interactive mode: read stdin line by line
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		if err := runAndSave(ctx, agentCfg, sess, line, historyMD); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		fmt.Print("\n> ")
	}
	return nil
}

func runAndSave(ctx context.Context, cfg agent.Config, sess *agent.Session, input, historyMD string) error {
	if err := agent.RunTurn(ctx, cfg, sess, input); err != nil {
		return err
	}
	if historyMD != "" {
		return history.Save(historyMD, sess.Messages)
	}
	return nil
}

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init [git-url]",
		Short: "Initialize .shell3/ project config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			if len(args) > 0 {
				return fmt.Errorf("git init not yet supported — coming soon")
			}
			return commands.InitProject(cwd)
		},
	}
}

func newAuthCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Configure LLM provider credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, _ := os.UserHomeDir()
			return commands.RunAuthInteractive(homeDir)
		},
	}
}
```

- [ ] **Step 2: Wire commands into main**

Replace `cmd/shell3/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable coding agent",
	}

	runCmd := newRunCommand()
	// make `shell3 "message"` work without explicit `run` subcommand
	root.RunE = runCmd.RunE
	root.Flags().AddFlagSet(runCmd.Flags())

	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Build and verify**

```bash
go build ./cmd/shell3/ && ./shell3 --help
```

Expected: help text showing flags and subcommands.

```bash
./shell3 init --help
./shell3 auth --help
```

- [ ] **Step 4: Run all tests**

```bash
go test ./... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/
git commit -m "feat: wire CLI commands — run, init, auth"
```

---

## Task 17: Smoke Test End-to-End

**Files:**
- Create: `test/smoke_test.go`

- [ ] **Step 1: Write smoke test**

Create `test/smoke_test.go`:

```go
//go:build smoke

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with: go test ./test/ -tags smoke -v
// Requires Ollama running locally.
func TestSmoke_InitAndRun(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()

	// write credentials
	credsDir := filepath.Join(homeDir, ".shell3")
	os.MkdirAll(credsDir, 0700)
	os.WriteFile(filepath.Join(credsDir, "credentials.yaml"), []byte(`
providers:
  ollama:
    base_url: http://localhost:11434/v1
`), 0600)

	// write project config
	shell3Dir := filepath.Join(dir, ".shell3")
	os.MkdirAll(shell3Dir, 0755)
	os.WriteFile(filepath.Join(shell3Dir, "config.yaml"), []byte(`
model: llama3.2
provider: ollama
`), 0644)

	binary := "../shell3"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("binary not built — run go build ./cmd/shell3/ first")
	}

	cmd := exec.Command(binary, "say hello in one word")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shell3 failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("expected non-empty output")
	}
	t.Logf("output: %s", out)
}
```

- [ ] **Step 2: Run unit tests (not smoke)**

```bash
go test ./... -v
```

Expected: all PASS.

- [ ] **Step 3: Build final binary**

```bash
go build -o shell3 ./cmd/shell3/
```

- [ ] **Step 4: Manual smoke test (if Ollama available)**

```bash
mkdir /tmp/test-shell3 && cd /tmp/test-shell3
shell3 init
shell3 auth   # enter: ollama, http://localhost:11434/v1, (empty key)
shell3 "what is 2+2"
```

Expected: agent responds with "4" or similar.

- [ ] **Step 5: Final commit**

```bash
git add test/ shell3
git commit -m "feat: add smoke test and built binary"
```

---

## Self-Review Notes

Spec coverage check:
- ✅ Single Go binary — `cmd/shell3/`
- ✅ OpenAI-compatible client — `internal/llm/client.go`
- ✅ bash tool — `internal/tools/bash.go`
- ✅ memory_search + memory_store — `internal/tools/memory.go`
- ✅ SQLite FTS5 — `internal/memory/memory.go`
- ✅ Markdown session history — `internal/history/history.go`
- ✅ Lifecycle hooks (shell commands, JSON pipe) — `internal/hooks/hooks.go`
- ✅ Skills loader — `internal/skills/skills.go`
- ✅ Plain text + JSONL output modes — `internal/output/emitter.go`
- ✅ `--stream`, `--out` flags — `cmd/shell3/run.go`
- ✅ `--history-md`, `--memory-db` flags — `cmd/shell3/run.go`
- ✅ `--personality`, `--no-bash`, `--no-memory-tools` flags
- ✅ Per-project `.shell3/` config — `internal/config/config.go`
- ✅ Global `~/.shell3/credentials.yaml` — `internal/config/credentials.go`
- ✅ Startup validation with clear error messages — `internal/config/validate.go`
- ✅ `shell3 init` command — `internal/commands/init.go`
- ✅ `shell3 auth` command — `internal/commands/auth.go`
- ✅ Interactive mode (stdin line by line) — `cmd/shell3/run.go`
- ✅ One-shot mode (arg) — `cmd/shell3/run.go`

Not implemented (non-goals):
- `--out CMD` subprocess spawning — OutEmitter is stubbed, full impl deferred
- `agent init <git-url>` — stubbed with "coming soon"
- Parallel tool execution — sequential only per spec
- Context compaction — deferred, hooks cover it via `on_context_build`
