package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

const minPruneBytes = 500

// PruneHandler implements the prune_tool_result built-in tool.
// It replaces a prior tool result in the conversation with a short stub,
// freeing context window space. Mutations propagate through the slice elements.
type PruneHandler struct{}

func (PruneHandler) Name() string { return "prune_tool_result" }

func (PruneHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handlePruneToolResultFrom(string(args), cfg.AllMsgs, cfg.SessMsgs), nil
}

func handlePruneToolResultFrom(rawArgs string, slices ...[]llm.Message) string {
	var args struct {
		ToolCallID string `json:"tool_call_id"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.ToolCallID == "" {
		return "error: tool_call_id required"
	}
	if args.Reason == "" {
		return "error: reason required"
	}
	stem := fmt.Sprintf("pruned: %s", args.Reason)
	return pruneByID(args.ToolCallID, stem, slices...)
}

func pruneByID(toolCallID, stem string, slices ...[]llm.Message) string {
	var target *llm.Message
	var name string
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				target = &msgs[i]
				name = msgs[i].Name
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("error: no tool result with id %q in conversation", toolCallID)
	}

	content := target.Content
	if len(content) < minPruneBytes {
		return fmt.Sprintf("error: result is %d bytes; below %d-byte prune threshold", len(content), minPruneBytes)
	}
	if looksLikeError(content) {
		return "error: refusing to prune a result that looks like a tool error"
	}

	stub := fmt.Sprintf("[%s — original was %d bytes]", stem, len(content))
	count := 0
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				msgs[i].Content = stub
				count++
			}
		}
	}
	if count == 0 {
		return "error: failed to update message content"
	}
	return fmt.Sprintf("Pruned result of %s (id=%s): freed %d bytes", name, toolCallID, len(content)-len(stub))
}

func looksLikeError(s string) bool {
	t := strings.TrimSpace(s)
	// Skip the synthetic [tool_call_id=...] header line if present so the
	// real payload's first line is what we inspect.
	if strings.HasPrefix(t, "[tool_call_id=") {
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = strings.TrimSpace(t[nl+1:])
		} else {
			return false
		}
	}
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	return strings.HasPrefix(low, "error:") || strings.HasPrefix(low, "error ")
}
