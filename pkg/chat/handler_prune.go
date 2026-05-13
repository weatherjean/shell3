package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/pkg/llm"
)

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
	// Scope model-driven prune to current + previous turn. The further back
	// the mutation lands, the more downstream turns must be re-processed on
	// the next request — capping the scope keeps that re-processing bounded.
	// Users can still prune any id via the /prune slash command, which
	// bypasses this scope.
	scoped := make([][]llm.Message, 0, len(slices))
	for _, s := range slices {
		scoped = append(scoped, lastNTurns(s, 2))
	}
	out := PruneByID(args.ToolCallID, stem, scoped...)
	if strings.HasPrefix(out, "error: no tool result with id") {
		// Distinguish out-of-scope from truly-absent by re-checking full slices.
		for _, s := range slices {
			for i := range s {
				if s[i].Role == llm.RoleTool && s[i].ToolCallID == args.ToolCallID {
					return fmt.Sprintf("error: tool result %q is older than the last 2 turns and cannot be pruned.", args.ToolCallID)
				}
			}
		}
	}
	return out
}

// lastNTurns returns the suffix of msgs starting at the n-th-from-last user
// message. A "turn" is bounded by user messages. If fewer than n user
// messages exist, the whole slice is returned. The returned slice shares
// the backing array with msgs, so element mutations propagate.
func lastNTurns(msgs []llm.Message, n int) []llm.Message {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			seen++
			if seen == n {
				return msgs[i:]
			}
		}
	}
	return msgs
}

// PruneByID replaces the tool result with the given id in any of the slices
// with a short stem stub. Returns a human-readable status string.
func PruneByID(toolCallID, stem string, slices ...[]llm.Message) string {
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
