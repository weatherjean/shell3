package codeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

// ANSI color helpers.
const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[34m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

// bashTool is the single tool shell3 code exposes to the model.
var bashTool = llm.ToolDefinition{
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

var storeToolDefs = []llm.ToolDefinition{
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
		Name:        "history_search",
		Description: "Search past conversation history for relevant context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "memory_list",
		Description: "List all stored memory entries.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "history_latest",
		Description: "Return the most recent conversation turns across all sessions.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// ExtractBashBlocks extracts the contents of all ```bash ... ``` blocks from text.
// Kept for testing; main loop uses tool_calls API.
func ExtractBashBlocks(text string) []string {
	var blocks []string
	parts := strings.Split(text, "```")
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
	if buf.Len() == 0 {
		return "(no output)"
	}
	return buf.String()
}

// LLMClient is the interface loop.go needs from the LLM layer.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds everything Run needs.
type Config struct {
	LLM           LLMClient
	Store         *store.Store
	WorkDir       string
	Provider      string
	Model         string
	Models        []string      // full list from config; Models[0] == Model at start
	ModelSwitcher func(string)  // called when /model changes the active model
}

// Run starts the interactive coding loop. Exits on ctrl+c at the prompt or io.EOF.
func Run(ctx context.Context, cfg Config) error {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: CodeSystemPrompt},
	}

	var sessionID int64
	if cfg.Store != nil {
		var err error
		sessionID, err = cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("code: start session: %w", err)
		}
		defer cfg.Store.EndSession(sessionID)
	}

	activeTools := []llm.ToolDefinition{bashTool}
	if cfg.Store != nil {
		activeTools = append(activeTools, storeToolDefs...)
	}

	fmt.Println(colorYellow + colorBold + "shell3 code" + colorReset)
	if cfg.Provider != "" {
		fmt.Printf(colorDim+"provider: %s"+colorReset+"\n", cfg.Provider)
	}
	if cfg.Model != "" {
		fmt.Printf(colorDim+"model:    %s"+colorReset+"\n", cfg.Model)
	}
	fmt.Println(colorDim + "type / for commands, ctrl+c to exit" + colorReset)

	var lastUsage llm.Usage
	truncate := true

	for {
		input, err := ReadInput()
		if err == io.EOF {
			fmt.Println()
			return nil
		}
		if err != nil {
			return err
		}

		if handled := handleSlashCommand(input, &cfg, &messages, &lastUsage, activeTools, &truncate); handled {
			continue
		}

		prevLen := len(messages)
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: input})
		messages = runTurn(ctx, cfg, messages, activeTools, &lastUsage, truncate)

		if cfg.Store != nil {
			for _, m := range messages[prevLen:] {
				switch m.Role {
				case llm.RoleUser, llm.RoleAssistant:
					cfg.Store.AppendHistory(sessionID, string(m.Role), m.Content)
					for _, tc := range m.ToolCalls {
						cfg.Store.AppendHistory(sessionID, "tool", toolCallSummary(tc))
					}
				}
			}
		}

		fmt.Println()
	}
}

// runTurn runs one user→assistant exchange using the tool_calls API.
// Returns updated message slice. ctrl+c cancels the turn.
func runTurn(ctx context.Context, cfg Config, messages []llm.Message, activeTools []llm.ToolDefinition, lastUsage *llm.Usage, truncate bool) []llm.Message {
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
		text, toolCalls, u, cancelled, err := streamTurn(turnCtx, cfg.LLM, messages, activeTools)
		if u != nil {
			*lastUsage = *u
		}
		if cancelled {
			fmt.Println(colorDim + "\n[cancelled]" + colorReset)
			return messages
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, colorRed+"\nerror: %v\n"+colorReset, err)
			fmt.Fprintf(os.Stderr, colorDim+"hint: use /prune to remove the last turn and retry\n"+colorReset)
			return messages
		}

		if text != "" || len(toolCalls) > 0 {
			assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: text}
			assistantMsg.ToolCalls = toolCalls
			messages = append(messages, assistantMsg)
		}

		if len(toolCalls) == 0 {
			return messages
		}

		// Execute each tool call and append results.
		for _, tc := range toolCalls {
			if turnCtx.Err() != nil {
				fmt.Println(colorDim + "[cancelled]" + colorReset)
				return messages
			}

			var out string
			if tc.Name == "bash" {
				command := parseCommand(tc.RawArgs)
				fmt.Printf(colorYellow+"$ %s"+colorReset+"\n", command)
				out = ExecuteBlock(turnCtx, command, cfg.WorkDir)
				if truncate {
					fmt.Print(truncateOutput(out))
				} else {
					fmt.Print(out)
				}
			} else {
				fmt.Printf(colorYellow+"→ %s(%s)"+colorReset+"\n", tc.Name, tc.RawArgs)
				out = dispatchStoreTool(tc.Name, tc.RawArgs, cfg.Store)
				fmt.Println(colorDim + out + colorReset)
			}

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    out,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
		}
	}
}

// streamTurn streams one LLM response, collecting text, tool calls, and usage.
func streamTurn(ctx context.Context, client LLMClient, messages []llm.Message, tools []llm.ToolDefinition) (text string, toolCalls []llm.ToolCall, usage *llm.Usage, cancelled bool, err error) {
	var sb strings.Builder
	labelPrinted := false
	streamErr := client.Stream(ctx, messages, tools, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			if !labelPrinted {
				fmt.Print("\n" + colorBlue + "shell3:" + colorReset + "\n")
				labelPrinted = true
			}
			fmt.Print(ev.TextDelta)
			sb.WriteString(ev.TextDelta)
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	})
	if ctx.Err() != nil {
		return sb.String(), toolCalls, usage, true, nil
	}
	return sb.String(), toolCalls, usage, false, streamErr
}

var slashCommands = []struct {
	name string
	desc string
}{
	{"/clear", "reset conversation context"},
	{"/prune", "remove last turn from context"},
	{"/model", "switch active model"},
	{"/usage", "show token usage from last turn"},
	{"/prompt", "dump system prompt and active tools"},
	{"/truncate", "toggle truncated bash output (default: on)"},
	{"/help", "list available commands"},
	{"/list", "list available commands"},
}

// handleSlashCommand processes /commands. Returns true if the input was handled.
func handleSlashCommand(input string, cfg *Config, messages *[]llm.Message, lastUsage *llm.Usage, activeTools []llm.ToolDefinition, truncate *bool) bool {
	if !strings.HasPrefix(input, "/") {
		return false
	}
	cmd := strings.TrimSpace(strings.ToLower(input))

	if cmd == "/" {
		cmd = pickSlashCommand()
		if cmd == "" {
			return true
		}
	}

	switch cmd {
	case "/clear":
		*messages = []llm.Message{{Role: llm.RoleSystem, Content: CodeSystemPrompt}}
		fmt.Print("\033[2J\033[H")
		fmt.Println(colorDim + "context cleared" + colorReset)
	case "/prune":
		pruned := pruneLastTurn(*messages)
		if len(pruned) == len(*messages) {
			fmt.Println(colorDim + "nothing to prune" + colorReset)
		} else {
			*messages = pruned
			fmt.Println(colorDim + "last turn removed from context" + colorReset)
		}
	case "/model":
		if len(cfg.Models) <= 1 {
			fmt.Println(colorDim + "only one model configured" + colorReset)
			break
		}
		chosen := pickModel(cfg.Models, cfg.Model)
		if chosen != "" && chosen != cfg.Model {
			cfg.Model = chosen
			if cfg.ModelSwitcher != nil {
				cfg.ModelSwitcher(chosen)
			}
			fmt.Printf(colorDim+"model: %s"+colorReset+"\n", chosen)
		}
	case "/usage":
		if lastUsage.TotalTokens == 0 {
			fmt.Println(colorDim + "no usage data yet" + colorReset)
		} else {
			fmt.Printf(colorBold+"token usage"+colorReset+" (last turn)\n")
			fmt.Printf("  prompt:     %d\n", lastUsage.PromptTokens)
			fmt.Printf("  completion: %d\n", lastUsage.CompletionTokens)
			fmt.Printf("  total:      %d\n", lastUsage.TotalTokens)
		}
	case "/prompt":
		fmt.Println(colorBold + "system prompt:" + colorReset)
		fmt.Println(colorDim + CodeSystemPrompt + colorReset)
		fmt.Println(colorBold + "active tools:" + colorReset)
		for _, t := range activeTools {
			fmt.Printf("  "+colorBlue+"%s"+colorReset+"  %s\n", t.Name, t.Description)
		}
	case "/truncate":
		*truncate = !*truncate
		state := "on"
		if !*truncate {
			state = "off"
		}
		fmt.Printf(colorDim+"truncate: %s"+colorReset+"\n", state)
	case "/help", "/list":
		fmt.Println(colorBold + "commands:" + colorReset)
		for _, c := range slashCommands {
			fmt.Printf("  "+colorBlue+"%s"+colorReset+"  %s\n", c.name, c.desc)
		}
	default:
		fmt.Printf(colorRed+"unknown command: %s  (type / to browse commands)"+colorReset+"\n", input)
	}
	return true
}

func pickModel(models []string, current string) string {
	var picked string
	options := make([]huh.Option[string], len(models))
	for i, m := range models {
		label := m
		if m == current {
			label += " (active)"
		}
		options[i] = huh.NewOption(label, m)
	}
	theme := huh.ThemeCharm()
	cyan := lipgloss.Color("6")
	theme.Focused.Title = theme.Focused.Title.Foreground(cyan)
	theme.Blurred.Title = theme.Blurred.Title.Foreground(cyan)
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("model").
				Options(options...).
				Value(&picked),
		),
	).WithTheme(theme)
	if err := form.Run(); err != nil {
		return ""
	}
	return picked
}

func pickSlashCommand() string {
	var picked string
	options := make([]huh.Option[string], len(slashCommands))
	for i, c := range slashCommands {
		options[i] = huh.NewOption(c.name+"  "+c.desc, c.name)
	}
	theme := huh.ThemeCharm()
	cyan := lipgloss.Color("6")
	theme.Focused.Title = theme.Focused.Title.Foreground(cyan)
	theme.Blurred.Title = theme.Blurred.Title.Foreground(cyan)
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("command").
				Options(options...).
				Value(&picked),
		),
	).WithTheme(theme)
	if err := form.Run(); err != nil {
		return ""
	}
	return picked
}

func parseCommand(rawArgs string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return rawArgs
	}
	return args.Command
}

// pruneLastTurn removes the last user message and everything that follows it
// (assistant reply, tool results) from the message slice.
func pruneLastTurn(messages []llm.Message) []llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			return messages[:i]
		}
	}
	return messages
}

func dispatchStoreTool(name, rawArgs string, st *store.Store) string {
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
		return fmt.Sprintf("Stored: %s", key)
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
		return fmt.Sprintf("Removed: %s", key)
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
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

// truncateOutput caps output at 10 lines or 1000 chars, whichever comes first.
func truncateOutput(s string) string {
	const maxLines = 10
	const maxBytes = 1000
	if len(s) > maxBytes {
		return s[:maxBytes] + fmt.Sprintf("\n"+colorDim+"… (+%d bytes)"+colorReset+"\n", len(s)-maxBytes)
	}
	lines := strings.SplitN(s, "\n", maxLines+2)
	if len(lines) > maxLines+1 {
		total := strings.Count(s, "\n") + 1
		return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n"+colorDim+"… (+%d lines)"+colorReset+"\n", total-maxLines)
	}
	return s
}

// toolCallSummary returns a compact 1-line summary of a tool call for history.
func toolCallSummary(tc llm.ToolCall) string {
	const maxLen = 80
	if tc.Name == "bash" {
		cmd := parseCommand(tc.RawArgs)
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
