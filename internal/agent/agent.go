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

// LLMClient is the interface the agent uses to stream LLM responses.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds all dependencies for a RunTurn call.
type Config struct {
	SystemPrompt string
	LLM          LLMClient
	Tools        []tools.Tool
	Hooks        *hooks.Runner
	Emitter      output.Emitter
}

// RunTurn executes one user→assistant turn, appending messages to sess.
func RunTurn(ctx context.Context, cfg Config, sess *Session, userInput string) error {
	if cfg.Hooks == nil {
		cfg.Hooks = hooks.NewRunner(hooks.Config{})
	}

	sess.Append(llm.Message{Role: llm.RoleUser, Content: userInput})

	msgs, err := cfg.Hooks.OnContextBuild(ctx, sess.Messages)
	if err != nil {
		msgs = sess.Messages
	}

	allMsgs := make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.SystemPrompt})
	allMsgs = append(allMsgs, msgs...)

	defs := make([]llm.ToolDefinition, len(cfg.Tools))
	for i, t := range cfg.Tools {
		defs[i] = t.Definition()
	}

	var responseText strings.Builder
	var pendingToolCalls []llm.ToolCall

	if err := cfg.LLM.Stream(ctx, allMsgs, defs, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			responseText.WriteString(ev.TextDelta)
			cfg.Emitter.Emit(output.Event{Type: output.EventToken, Text: ev.TextDelta})
		}
		if ev.ToolCall != nil {
			pendingToolCalls = append(pendingToolCalls, *ev.ToolCall)
		}
	}); err != nil {
		cfg.Emitter.Emit(output.Event{Type: output.EventError, Message: err.Error()})
		return err
	}

	for _, tc := range pendingToolCalls {
		cfg.Emitter.Emit(output.Event{Type: output.EventToolCall, Tool: tc.Name})

		var params map[string]any
		_ = json.Unmarshal([]byte(tc.RawArgs), &params)

		allowed, hookErr := cfg.Hooks.OnToolCall(ctx, tc.Name, params)
		if hookErr != nil || !allowed {
			result := fmt.Sprintf("Tool call blocked: %v", hookErr)
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
	if fullText != "" || len(pendingToolCalls) > 0 {
		sess.Append(llm.Message{Role: llm.RoleAssistant, Content: fullText})
	}
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
	return "", fmt.Errorf("agent: unknown tool: %s", name)
}
