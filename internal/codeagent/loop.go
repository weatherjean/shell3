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

	"github.com/weatherjean/shell3/internal/llm"
)

// ANSI color helpers.
const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorBlue   = "\033[34m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

const promptDivider = colorCyan + "——————————" + colorReset
const agentDivider = colorBlue + "——————————" + colorReset

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
	return buf.String()
}

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
func Run(ctx context.Context, cfg Config) error {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: CodeSystemPrompt},
	}

	fmt.Println(colorBold + "shell3 code" + colorReset + colorDim + " — type your request, ctrl+c to exit" + colorReset)
	fmt.Println()

	for {
		input, err := ReadInput()
		if err == io.EOF {
			fmt.Println()
			return nil
		}
		if err != nil {
			return err
		}

		fmt.Println(promptDivider)
		fmt.Println()

		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: input})
		messages = runTurn(ctx, cfg, messages)

		fmt.Println()
		fmt.Println(agentDivider)
		fmt.Println()
	}
}

// runTurn runs one user→assistant exchange using the tool_calls API.
// Returns updated message slice. ctrl+c cancels the turn.
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
		text, toolCalls, cancelled, err := streamTurn(turnCtx, cfg.LLM, messages)
		if cancelled {
			fmt.Println(colorDim + "\n[cancelled]" + colorReset)
			return messages
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, colorRed+"\nerror: %v\n"+colorReset, err)
			return messages
		}

		// Record the assistant message with any tool calls it made.
		// Some APIs reject null content on tool-call messages — use a space to satisfy omitempty.
		if text == "" && len(toolCalls) > 0 {
			text = " "
		}
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: text}
		assistantMsg.ToolCalls = toolCalls
		messages = append(messages, assistantMsg)

		if len(toolCalls) == 0 {
			return messages
		}

		// Execute each tool call and append results.
		for _, tc := range toolCalls {
			if turnCtx.Err() != nil {
				fmt.Println(colorDim + "[cancelled]" + colorReset)
				return messages
			}

			command := parseCommand(tc.RawArgs)
			fmt.Printf(colorYellow+"$ %s"+colorReset+"\n", command)

			out := ExecuteBlock(turnCtx, command, cfg.WorkDir)
			fmt.Print(out)

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    out,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
		}
	}
}

// streamTurn streams one LLM response, collecting text and tool calls.
func streamTurn(ctx context.Context, client LLMClient, messages []llm.Message) (text string, toolCalls []llm.ToolCall, cancelled bool, err error) {
	var sb strings.Builder
	labelPrinted := false
	streamErr := client.Stream(ctx, messages, []llm.ToolDefinition{bashTool}, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			if !labelPrinted {
				fmt.Print(colorBlue + "shell3:" + colorReset + "\n")
				labelPrinted = true
			}
			fmt.Print(ev.TextDelta)
			sb.WriteString(ev.TextDelta)
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
	})
	if ctx.Err() != nil {
		return sb.String(), toolCalls, true, nil
	}
	return sb.String(), toolCalls, false, streamErr
}

// parseCommand extracts the "command" field from the bash tool's JSON args.
func parseCommand(rawArgs string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return rawArgs
	}
	return args.Command
}
