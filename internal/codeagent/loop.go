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
