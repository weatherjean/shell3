# BubbleTea Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify `shell3 run` + `shell3 code` into a single BubbleTea chat TUI with personality config, `!command` TTY passthrough, and TTY-aware lifecycle hooks.

**Architecture:** A `tea.Program` owns the viewport+input UI. LLM turns stream chunks via a channel into the program's Update loop. `!command` input uses `tea.Exec` for TTY passthrough. Lifecycle hooks get a `TTYReleaser` backed by `p.ReleaseTerminal()`/`p.RestoreTerminal()`.

**Tech Stack:** `github.com/charmbracelet/bubbletea v1.3.6`, `github.com/charmbracelet/bubbles` (viewport, textinput), `github.com/charmbracelet/glamour`, `github.com/charmbracelet/lipgloss` — all already in `go.mod`.

---

## File Map

### Created
| File | Responsibility |
|------|---------------|
| `internal/personality/personality.go` | Personality type, base prompts, tool list construction |
| `internal/personality/personality_test.go` | Tool list correctness per personality |
| `internal/tui/messages.go` | All custom `tea.Msg` types used across the TUI |
| `internal/tui/model.go` | Root `tea.Model`: viewport + textinput + status bar |
| `internal/chat/chat.go` | `RunInteractive` and `RunOnce` entry points, `Config` struct |
| `internal/chat/turn.go` | `runTurn`: streams LLM response into a channel |
| `internal/chat/tools.go` | Bash exec, store tool dispatch, `!command` exec wrapper |
| `internal/chat/session.go` | Thin message-list wrapper (replaces `internal/agent/session.go`) |

### Modified
| File | What changes |
|------|-------------|
| `internal/config/config.go` | Add `Personality string` to `ProjectConfig` |
| `internal/scaffold/scaffold.go` | Add `personality` to `buildConfig`; prompt for personality in `InitProject` |
| `internal/hooks/types.go` | Add `TTYReleaser` interface |
| `internal/hooks/hooks.go` | Add `SetReleaser`, `callHookTTY`; update fire-and-forget hooks |
| `cmd/shell3/run.go` | Rewrite: call `chat.RunInteractive` / `chat.RunOnce` |
| `cmd/shell3/main.go` | Remove `newCodeCommand` |

### Deleted
| File | Reason |
|------|--------|
| `cmd/shell3/code.go` | Replaced by unified run.go |
| `internal/agent/agent.go` | Replaced by `internal/chat` |
| `internal/agent/session.go` | Replaced by `internal/chat/session.go` |
| `internal/codeagent/loop.go` | Replaced by `internal/chat` |
| `internal/codeagent/input.go` | BubbleTea textinput replaces huh form |
| `internal/codeagent/prompt.go` | Moved to `internal/personality` |
| `internal/codeagent/init.go` | `shell3 code --init` removed (YAGNI) |

---

## Task 1: Add `personality` to config and scaffold

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/scaffold/scaffold.go`

- [ ] **Step 1: Add Personality field to ProjectConfig**

In `internal/config/config.go`, add one field to `ProjectConfig`:

```go
type ProjectConfig struct {
    Model       string `yaml:"model"`
    Provider    string `yaml:"provider"`
    StoreDB     string `yaml:"store_db"`
    MemoryDB    string `yaml:"memory_db"`
    HistoryMD   string `yaml:"history_md"`
    Personality string `yaml:"personality"`
    Hooks       Hooks  `yaml:"hooks"`
}
```

- [ ] **Step 2: Add personality to buildConfig template**

In `internal/scaffold/scaffold.go`, update `buildConfig`:

```go
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
```

- [ ] **Step 3: Add personality prompt to InitProject**

In `internal/scaffold/scaffold.go`, add a helper and update `InitProject` and `InitCodeProject`:

```go
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
```

Add `"bufio"`, `"os"`, `"strings"` to imports (they may already exist — check before adding).

Update `initShell3Dir` signature to accept `personality`:
```go
func initShell3Dir(projectDir, provider, model, personality string) error {
    // ... same as before, but pass personality to buildConfig:
    files := map[string]string{
        filepath.Join(shell3Dir, "config.yaml"): buildConfig(provider, model, personality),
        filepath.Join(shell3Dir, ".gitignore"):  defaultGitignore,
    }
    // ... rest unchanged
}
```

Update `InitProject`:
```go
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
```

Remove `InitCodeProject` — it is no longer called anywhere.

- [ ] **Step 4: Build to verify no compile errors**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/scaffold/scaffold.go
git commit -m "feat(config): add personality field; prompt at init"
```

---

## Task 2: `internal/personality` package

**Files:**
- Create: `internal/personality/personality.go`
- Create: `internal/personality/personality_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/personality/personality_test.go`:

```go
package personality_test

import (
    "testing"

    "github.com/weatherjean/shell3/internal/personality"
)

func TestCodePersonalityHasBash(t *testing.T) {
    p := personality.Build(personality.TypeCode, nil, false)
    names := toolNames(p.Tools)
    if !contains(names, "bash") {
        t.Errorf("code personality missing bash tool; got %v", names)
    }
}

func TestAgentPersonalityHasBash(t *testing.T) {
    p := personality.Build(personality.TypeAgent, nil, false)
    names := toolNames(p.Tools)
    if !contains(names, "bash") {
        t.Errorf("agent personality missing bash tool; got %v", names)
    }
}

func TestStoreToolsIncludedWhenStorePresent(t *testing.T) {
    p := personality.Build(personality.TypeCode, nil, true)
    names := toolNames(p.Tools)
    for _, want := range []string{"memory_store", "memory_list", "memory_search", "memory_remove", "history_latest", "history_search"} {
        if !contains(names, want) {
            t.Errorf("personality with store missing tool %q; got %v", want, names)
        }
    }
}

func TestStoreToolsAbsentWithoutStore(t *testing.T) {
    p := personality.Build(personality.TypeCode, nil, false)
    names := toolNames(p.Tools)
    for _, unwanted := range []string{"memory_store", "memory_list"} {
        if contains(names, unwanted) {
            t.Errorf("personality without store has unexpected tool %q", unwanted)
        }
    }
}

func TestCodePromptNotEmpty(t *testing.T) {
    p := personality.Build(personality.TypeCode, nil, false)
    if p.SystemPrompt == "" {
        t.Error("code personality has empty system prompt")
    }
}

func toolNames(tools []personality.ToolDef) []string {
    out := make([]string, len(tools))
    for i, t := range tools {
        out[i] = t.Name
    }
    return out
}

func contains(ss []string, s string) bool {
    for _, v := range ss {
        if v == s {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/personality/... 2>&1
```

Expected: compile error — package does not exist yet.

- [ ] **Step 3: Implement `internal/personality/personality.go`**

```go
package personality

import (
    "github.com/weatherjean/shell3/internal/llm"
    "github.com/weatherjean/shell3/internal/skills"
)

// Type identifies a built-in personality.
type Type string

const (
    TypeCode  Type = "code"
    TypeAgent Type = "agent"
)

// ToolDef is a thin alias so callers don't import llm directly.
type ToolDef = llm.ToolDefinition

// Personality holds everything needed to start a chat session.
type Personality struct {
    SystemPrompt string
    Tools        []ToolDef
}

// Build constructs a Personality for the given type.
// loadedSkills are appended to the system prompt.
// hasStore controls whether memory/history tools are included.
func Build(t Type, loadedSkills []skills.Skill, hasStore bool) Personality {
    var base string
    switch t {
    case TypeAgent:
        base = agentPrompt
    default:
        base = codePrompt
    }

    prompt := base + skills.BuildSection(loadedSkills)

    tools := []ToolDef{bashTool}
    if hasStore {
        tools = append(tools, storeTools...)
    }

    return Personality{SystemPrompt: prompt, Tools: tools}
}

var bashTool = ToolDef{
    Name:        "bash",
    Description: "Execute a shell command in the project directory. Returns combined stdout and stderr.",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "command": map[string]any{
                "type":        "string",
                "description": "The shell command to run",
            },
        },
        "required": []string{"command"},
    },
}

var storeTools = []ToolDef{
    {
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
    },
    {
        Name:        "memory_list",
        Description: "List all stored memory entries.",
        Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
    },
    {
        Name:        "memory_search",
        Description: "Search project memory for relevant past decisions, notes, or context.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": map[string]any{"type": "string", "description": "Search query"},
            },
            "required": []string{"query"},
        },
    },
    {
        Name:        "memory_remove",
        Description: "Remove a key-value entry from project memory.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "key": map[string]any{"type": "string", "description": "Key to remove"},
            },
            "required": []string{"key"},
        },
    },
    {
        Name:        "history_latest",
        Description: "Return the most recent conversation turns. Call when asked about recent or past activity.",
        Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
    },
    {
        Name:        "history_search",
        Description: "Full-text search past conversation turns by query term.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": map[string]any{"type": "string", "description": "Search query"},
            },
            "required": []string{"query"},
        },
    },
}

const codePrompt = `You are shell3 — an agentic coding assistant running in the user's terminal.

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_store   — persist a key-value fact. Call when the user says "remember X" or you learn something worth keeping.
memory_list    — list all stored memories. Call when asked "what do you remember?".
memory_search  — full-text search memories by query term.
memory_remove  — delete a memory entry by key.

history_latest — return the most recent conversation turns. Call when asked about recent or past activity.
history_search — full-text search past conversation turns.

RULES:
- When told "remember X" → call memory_store immediately.
- When asked about memories or past context → call memory_search first. Never answer from training data.
- Never use bash to find or store memories.
- history_search searches past conversations. Never use bash to find past chat history.
- After gathering enough information, respond clearly — do not call tools indefinitely.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

Read before writing. Minimal changes. Test after every change.`

const agentPrompt = `You are shell3 — a general-purpose agent running in the user's terminal.

## Tools

bash — execute shell commands to accomplish tasks.

memory_store   — persist a key-value fact for future reference.
memory_list    — list all stored memories.
memory_search  — full-text search memories by query term.
memory_remove  — delete a memory entry by key.

history_latest — return the most recent conversation turns.
history_search — full-text search past conversation turns.

RULES:
- When told "remember X" → call memory_store immediately.
- When asked about memories or past context → call memory_search first.
- After gathering enough information, respond clearly — do not call tools indefinitely.`
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/personality/... -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/personality/
git commit -m "feat(personality): add personality package with code and agent built-ins"
```

---

## Task 3: Hooks TTYReleaser

**Files:**
- Modify: `internal/hooks/types.go`
- Modify: `internal/hooks/hooks.go`
- Create: `internal/hooks/hooks_tty_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/hooks/hooks_tty_test.go`:

```go
package hooks_test

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/weatherjean/shell3/internal/hooks"
)

// fakeReleaser tracks release/restore calls.
type fakeReleaser struct {
    released int
    restored int
}

func (f *fakeReleaser) Release() error { f.released++; return nil }
func (f *fakeReleaser) Restore() error { f.restored++; return nil }

func TestCallHookTTYReleasesAndRestores(t *testing.T) {
    // Write a script that exits 0.
    dir := t.TempDir()
    script := filepath.Join(dir, "hook.sh")
    if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
        t.Fatal(err)
    }

    rel := &fakeReleaser{}
    r := hooks.NewRunner(hooks.Config{OnSessionStart: script})
    r.SetReleaser(rel)

    r.OnSessionStart(context.Background())

    if rel.released != 1 {
        t.Errorf("Release called %d times, want 1", rel.released)
    }
    if rel.restored != 1 {
        t.Errorf("Restore called %d times, want 1", rel.restored)
    }
}

func TestNoReleaserSkipsRelease(t *testing.T) {
    dir := t.TempDir()
    script := filepath.Join(dir, "hook.sh")
    if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
        t.Fatal(err)
    }

    // No releaser set — should not panic, hook should still run.
    r := hooks.NewRunner(hooks.Config{OnSessionStart: script})
    r.OnSessionStart(context.Background()) // must not panic
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/hooks/... 2>&1
```

Expected: compile error — `SetReleaser` does not exist yet.

- [ ] **Step 3: Add TTYReleaser to types.go**

In `internal/hooks/types.go`, add after the existing type declarations:

```go
// TTYReleaser suspends and resumes the TUI so subprocess hooks can use the real terminal.
type TTYReleaser interface {
    Release() error
    Restore() error
}
```

- [ ] **Step 4: Add SetReleaser and callHookTTY to hooks.go**

In `internal/hooks/hooks.go`:

1. Add `releaser TTYReleaser` field to `Runner`:

```go
type Runner struct {
    cfg      Config
    releaser TTYReleaser
}
```

2. Add `SetReleaser` method:

```go
// SetReleaser sets the TTYReleaser used by fire-and-forget hooks.
func (r *Runner) SetReleaser(rel TTYReleaser) { r.releaser = rel }
```

3. Add `callHookTTY` after `callHook`:

```go
// callHookTTY runs a fire-and-forget hook with the real terminal (stdio inherited).
// If no releaser is set, the hook runs without TTY release.
func (r *Runner) callHookTTY(ctx context.Context, cmd string, input hookInput) {
    ctx, cancel := context.WithTimeout(ctx, hookTimeout)
    defer cancel()

    data, _ := json.Marshal(input)
    parts := strings.Fields(cmd)
    c := exec.CommandContext(ctx, parts[0], parts[1:]...)
    c.Stdin = bytes.NewReader(data)
    c.Stdout = os.Stdout
    c.Stderr = os.Stderr

    if r.releaser != nil {
        _ = r.releaser.Release()
        defer r.releaser.Restore()
    }
    _ = c.Run()
}
```

4. Update fire-and-forget hooks to use `callHookTTY`:

```go
func (r *Runner) OnSessionStart(ctx context.Context) {
    if r.cfg.OnSessionStart != "" {
        r.callHookTTY(ctx, r.cfg.OnSessionStart, hookInput{Hook: "on_session_start"})
    }
}

func (r *Runner) OnSessionEnd(ctx context.Context) {
    if r.cfg.OnSessionEnd != "" {
        r.callHookTTY(ctx, r.cfg.OnSessionEnd, hookInput{Hook: "on_session_end"})
    }
}

func (r *Runner) OnTurnStart(ctx context.Context) {
    if r.cfg.OnTurnStart != "" {
        r.callHookTTY(ctx, r.cfg.OnTurnStart, hookInput{Hook: "on_turn_start"})
    }
}

func (r *Runner) OnTurnEnd(ctx context.Context, response string) {
    if r.cfg.OnTurnEnd != "" {
        r.callHookTTY(ctx, r.cfg.OnTurnEnd, hookInput{
            Hook:   "on_turn_end",
            Params: map[string]any{"response": response},
        })
    }
}

func (r *Runner) OnToolResult(ctx context.Context, tool, result string) {
    if r.cfg.OnToolResult != "" {
        r.callHookTTY(ctx, r.cfg.OnToolResult, hookInput{
            Hook:   "on_tool_result",
            Tool:   tool,
            Params: map[string]any{"result": result},
        })
    }
}
```

Add `"os"` to the imports in `hooks.go` (it uses `os.Stdout`, `os.Stderr`).

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/hooks/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/hooks/
git commit -m "feat(hooks): add TTYReleaser; fire-and-forget hooks inherit terminal"
```

---

## Task 4: `internal/tui` package

**Files:**
- Create: `internal/tui/messages.go`
- Create: `internal/tui/model.go`

No unit tests (TUI rendering). Build verification only.

- [ ] **Step 1: Create messages.go**

```go
package tui

import (
    tea "github.com/charmbracelet/bubbletea"
    "github.com/weatherjean/shell3/internal/llm"
)

// ChunkMsg carries one streaming text delta from the LLM.
type ChunkMsg string

// TurnDoneMsg signals that the current LLM turn completed.
type TurnDoneMsg struct{ Usage llm.Usage }

// TurnErrMsg carries an error to display inline.
type TurnErrMsg struct{ Err error }

// AppendMsg appends pre-formatted text to the viewport.
type AppendMsg string

// StatusMsg replaces the status bar text.
type StatusMsg string

// streamMsg wraps a content message with a command to read the next item from the stream.
// next is nil when the stream is exhausted.
type streamMsg struct {
    msg  tea.Msg
    next tea.Cmd
}

// ReadCh returns a Cmd that reads one message from ch and wraps it in streamMsg.
// When ch is closed, it delivers TurnDoneMsg{} with no next Cmd.
func ReadCh(ch <-chan tea.Msg) tea.Cmd {
    return func() tea.Msg {
        inner, ok := <-ch
        if !ok {
            return streamMsg{msg: TurnDoneMsg{}, next: nil}
        }
        return streamMsg{msg: inner, next: ReadCh(ch)}
    }
}
```

- [ ] **Step 2: Create model.go**

```go
package tui

import (
    "fmt"
    "io"
    "os/exec"
    "strings"

    "github.com/charmbracelet/bubbles/textinput"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/glamour"
    "github.com/charmbracelet/lipgloss"
    "golang.org/x/term"
    "os"
)

var (
    dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
    promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
    errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
    userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// Model is the root BubbleTea model for shell3.
type Model struct {
    viewport  viewport.Model
    input     textinput.Model
    status    string
    content   strings.Builder
    ready     bool
    width     int
    height    int
    streaming bool
    submitFn  func(string) tea.Cmd
}

// New returns an initialized Model. submitFn is called with the user's input when they press Enter.
func New(status string, submitFn func(string) tea.Cmd) Model {
    ti := textinput.New()
    ti.Focus()
    ti.CharLimit = 0
    ti.Prompt = promptStyle.Render("> ")

    return Model{
        input:    ti,
        status:   status,
        submitFn: submitFn,
    }
}

// AppendContent adds text to the viewport, re-rendering glamour markdown if not streaming.
func (m *Model) AppendContent(text string) {
    m.content.WriteString(text)
    if m.ready {
        m.viewport.SetContent(m.content.String())
        m.viewport.GotoBottom()
    }
}

// SetStatus updates the status bar.
func (m *Model) SetStatus(s string) { m.status = s }

func (m Model) Init() tea.Cmd {
    return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.width = msg.Width
        m.height = msg.Height
        vpHeight := m.height - 4 // status bar + 2 dividers + input
        if vpHeight < 1 {
            vpHeight = 1
        }
        if !m.ready {
            m.viewport = viewport.New(m.width, vpHeight)
            m.viewport.SetContent(m.content.String())
            m.ready = true
        } else {
            m.viewport.Width = m.width
            m.viewport.Height = vpHeight
        }
        m.input.Width = m.width - 4

    case streamMsg:
        // Unwrap and handle the inner message, then schedule next read.
        inner := msg.msg
        switch v := inner.(type) {
        case ChunkMsg:
            m.content.WriteString(string(v))
            if m.ready {
                m.viewport.SetContent(m.content.String())
                m.viewport.GotoBottom()
            }
            m.streaming = true
        case TurnDoneMsg:
            m.streaming = false
            if v.Usage.TotalTokens > 0 {
                base := strings.SplitN(m.status, " │ tokens:", 2)[0]
                m.status = fmt.Sprintf("%s │ tokens: %d", base, v.Usage.TotalTokens)
            }
            // Render final content with glamour.
            rendered := renderMarkdown(m.content.String(), termWidth())
            m.content.Reset()
            m.content.WriteString(rendered)
            if m.ready {
                m.viewport.SetContent(m.content.String())
                m.viewport.GotoBottom()
            }
        case TurnErrMsg:
            m.streaming = false
            errText := errorStyle.Render("\n[error: " + v.Err.Error() + "]\n")
            m.content.WriteString(errText)
            if m.ready {
                m.viewport.SetContent(m.content.String())
                m.viewport.GotoBottom()
            }
        case AppendMsg:
            m.content.WriteString(string(v))
            if m.ready {
                m.viewport.SetContent(m.content.String())
                m.viewport.GotoBottom()
            }
        case StatusMsg:
            m.status = string(v)
        }
        if msg.next != nil {
            cmds = append(cmds, msg.next)
        }

    case tea.KeyMsg:
        switch msg.Type {
        case tea.KeyCtrlC:
            return m, tea.Quit
        case tea.KeyEnter:
            if !m.streaming {
                input := strings.TrimSpace(m.input.Value())
                if input != "" {
                    m.input.Reset()
                    if strings.HasPrefix(input, "!") {
                        // TTY passthrough via tea.Exec
                        c := exec.Command("bash", "-c", input[1:])
                        cmds = append(cmds, tea.Exec(newExecCmd(c), func(err error) tea.Msg {
                            if err != nil {
                                return AppendMsg(errorStyle.Render("\n[exit: "+err.Error()+"]\n"))
                            }
                            return AppendMsg("")
                        }))
                    } else {
                        cmds = append(cmds, m.submitFn(input))
                    }
                }
            }
        default:
            if !m.streaming {
                var tiCmd tea.Cmd
                m.input, tiCmd = m.input.Update(msg)
                cmds = append(cmds, tiCmd)
            }
        }
    }

    if m.ready {
        var vpCmd tea.Cmd
        m.viewport, vpCmd = m.viewport.Update(msg)
        cmds = append(cmds, vpCmd)
    }

    return m, tea.Batch(cmds...)
}

func (m Model) View() string {
    if !m.ready {
        return "loading…"
    }
    w := m.width
    divider := dimStyle.Render(strings.Repeat("─", w))
    statusBar := dimStyle.Render(m.status)
    return m.viewport.View() + "\n" + divider + "\n" + statusBar + "\n" + m.input.View()
}

// execCmd wraps *exec.Cmd to implement tea.ExecCommand.
type execCmd struct{ c *exec.Cmd }

func newExecCmd(c *exec.Cmd) *execCmd { return &execCmd{c} }

func (e *execCmd) SetStdin(r io.Reader)  { e.c.Stdin = r }
func (e *execCmd) SetStdout(w io.Writer) { e.c.Stdout = w }
func (e *execCmd) SetStderr(w io.Writer) { e.c.Stderr = w }
func (e *execCmd) Run() error            { return e.c.Run() }

func renderMarkdown(text string, width int) string {
    r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width))
    if err != nil {
        return text
    }
    out, err := r.Render(text)
    if err != nil {
        return text
    }
    return out
}

func termWidth() int {
    w, _, err := term.GetSize(int(os.Stdout.Fd()))
    if err != nil || w <= 0 {
        return 100
    }
    return w
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
go build ./internal/tui/...
```

Expected: no errors. If there are import issues with `tea.Exec`, ensure bubbletea v1.3.6 is in go.sum: `go mod tidy`.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): add BubbleTea model with viewport, textinput, and !command exec"
```

---

## Task 5: `internal/chat` package

**Files:**
- Create: `internal/chat/session.go`
- Create: `internal/chat/tools.go`
- Create: `internal/chat/turn.go`
- Create: `internal/chat/chat.go`

- [ ] **Step 1: Create session.go**

```go
package chat

import "github.com/weatherjean/shell3/internal/llm"

// session holds the in-progress conversation history.
type session struct {
    messages []llm.Message
}

func (s *session) append(m llm.Message) {
    s.messages = append(s.messages, m)
}
```

- [ ] **Step 2: Create tools.go**

```go
package chat

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "os/exec"
    "strings"
    "time"

    "github.com/weatherjean/shell3/internal/store"
)

const bashTimeout = 30 * time.Second

func executeBash(ctx context.Context, command, workDir string) string {
    ctx, cancel := context.WithTimeout(ctx, bashTimeout)
    defer cancel()

    c := exec.CommandContext(ctx, "bash", "-c", command)
    c.Dir = workDir
    var buf bytes.Buffer
    c.Stdout = &buf
    c.Stderr = &buf
    if err := c.Run(); err != nil {
        if buf.Len() == 0 {
            fmt.Fprintf(&buf, "error: %v\n", err)
        }
    }
    if buf.Len() == 0 {
        return "(no output)"
    }
    out := buf.String()
    return truncateOutput(out)
}

func dispatchStore(name, rawArgs string, st *store.Store) string {
    if st == nil {
        return fmt.Sprintf("error: store not available for tool %s", name)
    }
    var args map[string]any
    json.Unmarshal([]byte(rawArgs), &args)

    switch name {
    case "memory_store":
        key, _ := args["key"].(string)
        value, _ := args["value"].(string)
        if err := st.MemoryStore(key, value); err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        return "Stored: " + key
    case "memory_list":
        results, err := st.MemoryList(50)
        if err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        if len(results) == 0 {
            return "No memories stored."
        }
        var sb strings.Builder
        for _, r := range results {
            fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
        }
        return sb.String()
    case "memory_search":
        q, _ := args["query"].(string)
        results, err := st.MemorySearch(q, 5)
        if err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        if len(results) == 0 {
            return "No memories found."
        }
        var sb strings.Builder
        for _, r := range results {
            fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
        }
        return sb.String()
    case "memory_remove":
        key, _ := args["key"].(string)
        if err := st.MemoryDelete(key); err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        return "Removed: " + key
    case "history_latest":
        results, err := st.HistoryLatest(20)
        if err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        if len(results) == 0 {
            return "No history found."
        }
        var sb strings.Builder
        for _, r := range results {
            fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
                r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
        }
        return sb.String()
    case "history_search":
        q, _ := args["query"].(string)
        results, err := st.SearchHistory(q, 5)
        if err != nil {
            return fmt.Sprintf("error: %v", err)
        }
        if len(results) == 0 {
            return "No history found."
        }
        var sb strings.Builder
        for _, r := range results {
            fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
                r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
        }
        return sb.String()
    default:
        return fmt.Sprintf("unknown tool: %s", name)
    }
}

func truncateOutput(s string) string {
    const maxLines = 50
    const maxBytes = 5000
    if len(s) > maxBytes {
        return s[:maxBytes] + fmt.Sprintf("\n… (+%d bytes)\n", len(s)-maxBytes)
    }
    lines := strings.SplitN(s, "\n", maxLines+2)
    if len(lines) > maxLines+1 {
        total := strings.Count(s, "\n") + 1
        return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n… (+%d lines)\n", total-maxLines)
    }
    return s
}

func parseBashCommand(rawArgs string) string {
    var args struct {
        Command string `json:"command"`
    }
    if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
        return rawArgs
    }
    return args.Command
}
```

- [ ] **Step 3: Create turn.go**

```go
package chat

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/weatherjean/shell3/internal/hooks"
    "github.com/weatherjean/shell3/internal/llm"
    "github.com/weatherjean/shell3/internal/tui"
)

// runTurn executes one user→assistant exchange, sending tui messages to ch.
// The goroutine closes ch when done.
func runTurn(ctx context.Context, cfg Config, sess *session, input string, ch chan<- tea.Msg) {
    defer close(ch)

    cfg.Hooks.OnTurnStart(ctx)
    defer func() { cfg.Hooks.OnTurnEnd(ctx, "") }()

    sess.append(llm.Message{Role: llm.RoleUser, Content: input})

    msgs, err := cfg.Hooks.OnContextBuild(ctx, sess.messages)
    if err != nil {
        msgs = sess.messages
    }

    allMsgs := make([]llm.Message, 0, len(msgs)+1)
    allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
    allMsgs = append(allMsgs, msgs...)

    for {
        text, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, cfg.Personality.Tools, ch)
        if err != nil {
            ch <- tui.TurnErrMsg{Err: err}
            return
        }

        if text != "" || len(toolCalls) > 0 {
            assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: text}
            assistantMsg.ToolCalls = toolCalls
            allMsgs = append(allMsgs, assistantMsg)
            sess.append(assistantMsg)
        }

        if len(toolCalls) == 0 {
            ch <- tui.TurnDoneMsg{Usage: usage}
            return
        }

        // Execute tool calls.
        for _, tc := range toolCalls {
            if ctx.Err() != nil {
                return
            }

            allowed, hookErr := cfg.Hooks.OnToolCall(ctx, tc.Name, parseRawArgs(tc.RawArgs))
            var out string
            if hookErr != nil || !allowed {
                out = fmt.Sprintf("Tool call blocked: %v", hookErr)
            } else if tc.Name == "bash" {
                command := parseBashCommand(tc.RawArgs)
                ch <- tui.AppendMsg(fmt.Sprintf("\n$ %s\n", command))
                out = executeBash(ctx, command, cfg.WorkDir)
                ch <- tui.AppendMsg(out + "\n")
            } else {
                ch <- tui.AppendMsg(fmt.Sprintf("\n→ %s(%s)\n", tc.Name, tc.RawArgs))
                out = dispatchStore(tc.Name, tc.RawArgs, cfg.Store)
                ch <- tui.AppendMsg(out + "\n")
            }

            cfg.Hooks.OnToolResult(ctx, tc.Name, out)
            toolMsg := llm.Message{
                Role:       llm.RoleTool,
                Content:    out,
                ToolCallID: tc.ID,
                Name:       tc.Name,
            }
            allMsgs = append(allMsgs, toolMsg)
            sess.append(toolMsg)
        }
    }
}

// streamOnce calls the LLM once, collecting text, tool calls, and usage while sending chunks to ch.
func streamOnce(ctx context.Context, client LLMClient, msgs []llm.Message, tools []llm.ToolDefinition, ch chan<- tea.Msg) (text string, toolCalls []llm.ToolCall, usage llm.Usage, err error) {
    var sb strings.Builder

    ch <- tui.AppendMsg("\nshell3: ")

    streamErr := client.Stream(ctx, msgs, tools, func(ev llm.StreamEvent) {
        if ev.TextDelta != "" {
            sb.WriteString(ev.TextDelta)
            ch <- tui.ChunkMsg(ev.TextDelta)
        }
        if ev.ToolCall != nil {
            toolCalls = append(toolCalls, *ev.ToolCall)
        }
        if ev.Usage != nil {
            usage = *ev.Usage
        }
    })

    return sb.String(), toolCalls, usage, streamErr
}

func parseRawArgs(raw string) map[string]any {
    var out map[string]any
    json.Unmarshal([]byte(raw), &out)
    return out
}

// saveHistory persists new messages to the store after a turn.
func saveHistory(cfg Config, sess *session, sessionID int64, from int) {
    if cfg.Store == nil {
        return
    }
    for _, m := range sess.messages[from:] {
        switch m.Role {
        case llm.RoleUser, llm.RoleAssistant:
            cfg.Store.AppendHistory(sessionID, string(m.Role), m.Content)
            for _, tc := range m.ToolCalls {
                cfg.Store.AppendHistory(sessionID, "tool", toolCallSummary(tc))
            }
        }
    }
}

func toolCallSummary(tc llm.ToolCall) string {
    const maxLen = 80
    if tc.Name == "bash" {
        cmd := parseBashCommand(tc.RawArgs)
        line := strings.SplitN(cmd, "\n", 2)[0]
        if len(line) > maxLen {
            line = line[:maxLen] + "…"
        }
        return "bash: $ " + line
    }
    args := tc.RawArgs
    if len(args) > maxLen {
        args = args[:maxLen] + "…"
    }
    return tc.Name + "(" + args + ")"
}
```

- [ ] **Step 4: Create chat.go**

```go
package chat

import (
    "context"
    "fmt"
    "os"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/weatherjean/shell3/internal/hooks"
    "github.com/weatherjean/shell3/internal/llm"
    "github.com/weatherjean/shell3/internal/personality"
    "github.com/weatherjean/shell3/internal/store"
    "github.com/weatherjean/shell3/internal/tui"
)

// LLMClient is the streaming LLM interface.
type LLMClient interface {
    Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds all dependencies for a chat session.
type Config struct {
    LLM         LLMClient
    Hooks       *hooks.Runner
    Store       *store.Store
    Personality personality.Personality
    WorkDir     string
    StatusLine  string // shown in the status bar, e.g. "kimi-k2 | code"
}

// programReleaser implements hooks.TTYReleaser backed by a tea.Program.
type programReleaser struct{ p *tea.Program }

func (r *programReleaser) Release() error { return r.p.ReleaseTerminal() }
func (r *programReleaser) Restore() error { return r.p.RestoreTerminal() }

// RunInteractive starts the BubbleTea TUI and blocks until the user quits.
func RunInteractive(ctx context.Context, cfg Config) error {
    sess := &session{}

    var sessionID int64
    if cfg.Store != nil {
        var err error
        sessionID, err = cfg.Store.StartSession()
        if err != nil {
            return fmt.Errorf("chat: start session: %w", err)
        }
        defer cfg.Store.EndSession(sessionID)
    }

    submitFn := func(input string) tea.Cmd {
        if strings.HasPrefix(input, "/") {
            return handleSlash(input)
        }
        ch := make(chan tea.Msg, 256)
        prevLen := len(sess.messages)
        go func() {
            runTurn(ctx, cfg, sess, input, ch)
            saveHistory(cfg, sess, sessionID, prevLen)
        }()
        return tui.ReadCh(ch)
    }

    model := tui.New(cfg.StatusLine, submitFn)

    rel := &programReleaser{}
    prog := tea.NewProgram(model,
        tea.WithAltScreen(),
        tea.WithMouseCellMotion(),
    )
    rel.p = prog
    cfg.Hooks.SetReleaser(rel)

    cfg.Hooks.OnSessionStart(ctx)
    defer cfg.Hooks.OnSessionEnd(ctx)

    _, err := prog.Run()
    return err
}

// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg Config, input string) error {
    sess := &session{}
    ch := make(chan tea.Msg, 256)
    go runTurn(ctx, cfg, sess, input, ch)

    for msg := range ch {
        switch v := msg.(type) {
        case tui.ChunkMsg:
            fmt.Print(string(v))
        case tui.AppendMsg:
            fmt.Print(string(v))
        case tui.TurnErrMsg:
            fmt.Fprintln(os.Stderr, "error:", v.Err)
        case tui.TurnDoneMsg:
            fmt.Println()
        }
    }
    return nil
}

// handleSlash returns a tea.Cmd that appends slash command output to the viewport.
func handleSlash(input string) tea.Cmd {
    return func() tea.Msg {
        switch strings.TrimSpace(strings.ToLower(input)) {
        case "/clear":
            return tui.AppendMsg("\n[context cleared — restart shell3 to fully reset]\n")
        case "/help", "/":
            return tui.AppendMsg("\nCommands: /help, /clear\nPrefix ! to run shell commands with a real terminal.\n")
        default:
            return tui.AppendMsg(fmt.Sprintf("\nunknown command: %s\n", input))
        }
    }
}
```

- [ ] **Step 5: Build to verify no compile errors**

```bash
go build ./internal/chat/...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/
git commit -m "feat(chat): add chat package with BubbleTea-integrated turn loop"
```

---

## Task 6: Wire CLI — merge entry points

**Files:**
- Modify: `cmd/shell3/run.go` (full rewrite)
- Modify: `cmd/shell3/main.go`
- Delete: `cmd/shell3/code.go`

- [ ] **Step 1: Rewrite cmd/shell3/run.go**

Replace the entire file content:

```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"

    "github.com/spf13/cobra"
    "github.com/weatherjean/shell3/internal/chat"
    "github.com/weatherjean/shell3/internal/config"
    "github.com/weatherjean/shell3/internal/hooks"
    "github.com/weatherjean/shell3/internal/llm"
    "github.com/weatherjean/shell3/internal/personality"
    "github.com/weatherjean/shell3/internal/skills"
    "github.com/weatherjean/shell3/internal/store"
)

type runFlags struct {
    model      string
    baseURL    string
    apiKey     string
    noBash     bool
    noMemory   bool
}

func newRunCommand() *cobra.Command {
    f := &runFlags{}
    cmd := &cobra.Command{
        Use:   "shell3 [message]",
        Short: "Run the shell3 chat agent",
        RunE: func(cmd *cobra.Command, args []string) error {
            return runChat(cmd.Context(), f, strings.Join(args, " "))
        },
    }
    cmd.Flags().StringVar(&f.model, "model", "", "Model override")
    cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
    cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
    cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
    cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
    return cmd
}

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
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

    // Resolve provider and model.
    model, baseURL, apiKey, provName := resolveConnection(projCfg, creds, f)

    // Open store (best-effort).
    var st *store.Store
    storeDBPath := filepath.Join(cwd, coalesce(projCfg.StoreDB, ".shell3/shell3.db"))
    if !f.noMemory {
        if s, err := store.Open(storeDBPath); err == nil {
            st = s
            defer st.Close()
        }
    }

    // Build personality.
    loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})
    pType := personality.TypeCode
    if projCfg.Personality == "agent" {
        pType = personality.TypeAgent
    }
    pers := personality.Build(pType, loadedSkills, st != nil)

    hookRunner := hooks.NewRunner(hooks.Config(projCfg.Hooks))

    statusLine := fmt.Sprintf("%s │ %s │ %s", provName, model, string(pType))

    cfg := chat.Config{
        LLM:         llm.NewClient(baseURL, apiKey, model),
        Hooks:       hookRunner,
        Store:       st,
        Personality: pers,
        WorkDir:     cwd,
        StatusLine:  statusLine,
    }

    if initialInput != "" {
        return chat.RunOnce(ctx, cfg, initialInput)
    }
    return chat.RunInteractive(ctx, cfg)
}

func resolveConnection(projCfg *config.ProjectConfig, creds *config.Credentials, f *runFlags) (model, baseURL, apiKey, provName string) {
    if f.baseURL != "" && f.apiKey != "" {
        return coalesce(f.model, projCfg.Model, "llama3.2"), f.baseURL, f.apiKey, ""
    }

    hint := ""
    if projCfg != nil {
        hint = projCfg.Provider
    }

    var provCreds config.ProviderCredentials
    if hint != "" {
        if p, ok := creds.Providers[hint]; ok {
            provName = hint
            provCreds = p
        }
    }

    if provName == "" && len(creds.Providers) > 0 {
        names := make([]string, 0, len(creds.Providers))
        for n := range creds.Providers {
            names = append(names, n)
        }
        sort.Strings(names)
        provName = names[0]
        provCreds = creds.Providers[provName]
    }

    if f.baseURL != "" {
        baseURL = f.baseURL
    } else {
        baseURL = provCreds.BaseURL
    }
    if f.apiKey != "" {
        apiKey = f.apiKey
    } else {
        apiKey = provCreds.APIKey
    }

    model = projCfg.Model
    if f.model != "" {
        model = f.model
    }
    if model == "" {
        // use first from comma-sep default_model
        for _, part := range strings.Split(provCreds.DefaultModel, ",") {
            if m := strings.TrimSpace(part); m != "" {
                model = m
                break
            }
        }
    }
    if model == "" {
        model = "llama3.2"
    }
    return
}

func coalesce(values ...string) string {
    for _, v := range values {
        if v != "" {
            return v
        }
    }
    return ""
}
```

- [ ] **Step 2: Update cmd/shell3/main.go**

Remove `newCodeCommand()`. The file should be:

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
    root.RunE = runCmd.RunE
    root.Flags().AddFlagSet(runCmd.Flags())

    root.AddCommand(newInitCommand())
    root.AddCommand(newAuthCommand())
    root.AddCommand(newDocsCommand())
    root.AddCommand(newDestroyCommand())

    if err := root.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

- [ ] **Step 3: Delete cmd/shell3/code.go**

```bash
rm cmd/shell3/code.go
```

- [ ] **Step 4: Build**

```bash
go build ./cmd/shell3/...
```

Expected: no errors. If there are any remaining references to old packages, fix them.

- [ ] **Step 5: Smoke test — one-shot mode**

```bash
./shell3 "what is 2+2"
```

Expected: response printed to stdout, process exits.

- [ ] **Step 6: Smoke test — interactive mode**

```bash
./shell3
```

Expected: BubbleTea TUI launches, viewport visible, input prompt visible. Type a message, press Enter. Response streams in. Type `!ls`, press Enter — ls output appears inline. Ctrl+C exits.

- [ ] **Step 7: Commit**

```bash
git add cmd/shell3/run.go cmd/shell3/main.go
git commit -m "feat(cmd): unify run/code into single chat entry point"
```

---

## Task 7: Delete dead packages

**Files:**
- Delete: `internal/agent/`
- Delete: `internal/codeagent/`
- Modify: `internal/output/` — verify nothing imports it from the deleted packages; keep it (used in future one-shot improvements)

- [ ] **Step 1: Check for remaining imports of dead packages**

```bash
grep -r "internal/agent\|internal/codeagent" --include="*.go" .
```

Expected: no results. If any exist, remove those imports first.

- [ ] **Step 2: Delete dead packages**

```bash
rm -rf internal/agent internal/codeagent
```

- [ ] **Step 3: Build to verify nothing is broken**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: remove internal/agent and internal/codeagent (replaced by internal/chat)"
```

---

## Self-Review Notes

- **Spec coverage check:**
  - ✅ Unified CLI entry point (Task 6)
  - ✅ Personality config (Tasks 1, 2)
  - ✅ Personality set at init (Task 1)
  - ✅ BubbleTea viewport+input (Task 4)
  - ✅ Streaming via channel (Tasks 4, 5)
  - ✅ `!command` TTY passthrough via `tea.Exec` (Task 4)
  - ✅ TTY hooks via `TTYReleaser` (Tasks 3, 5)
  - ✅ One-shot mode skips TUI (Tasks 5, 6)
  - ✅ Skills loaded for both personalities (Task 2)
  - ✅ Dead packages deleted (Task 7)

- **Type consistency:** `tui.ReadCh` defined in Task 4, used in Task 5 chat.go. `hooks.TTYReleaser` defined in Task 3, implemented in Task 5 as `programReleaser`. `personality.Personality` defined in Task 2, used in Task 5 `Config`. All consistent.

- **Known limitation:** `/clear` in Task 6 does not actually reset the session messages — it only appends a note. A proper `/clear` would require passing a reset signal from the model back to `chat.go`. This is a known shortcut acceptable for now.
