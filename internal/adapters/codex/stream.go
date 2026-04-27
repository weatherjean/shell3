package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// parseStream consumes Server-Sent Events from r and translates them into
// llm.StreamEvent values delivered via onEvent. The Responses API emits one
// JSON envelope per `data:` line; the envelope's `type` field selects the
// event variant. Unknown types are ignored to stay compatible across server
// versions.
//
// At end of stream a final {Done: true} event is emitted exactly once.
func parseStream(r io.Reader, onEvent func(llm.StreamEvent)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Tool calls accumulate across multiple delta events keyed by item_id.
	// Some servers emit `call_id` only on the final `output_item.done`, so we
	// capture both names and reconcile at flush time.
	type pending struct {
		ItemID string
		CallID string
		Name   string
		Args   strings.Builder
	}
	tools := map[string]*pending{}

	emitTool := func(p *pending) {
		id := p.CallID
		if id == "" {
			id = p.ItemID
		}
		onEvent(llm.StreamEvent{ToolCall: &llm.ToolCall{
			ID:      id,
			Name:    p.Name,
			RawArgs: p.Args.String(),
		}})
	}

	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "" || payload == "[DONE]" {
			continue
		}

		var env struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			ItemID   string          `json:"item_id"`
			Item     json.RawMessage `json:"item"`
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue
		}

		switch env.Type {
		case "response.output_text.delta":
			if env.Delta != "" {
				onEvent(llm.StreamEvent{TextDelta: env.Delta})
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if env.Delta != "" {
				onEvent(llm.StreamEvent{ReasoningDelta: env.Delta})
			}

		case "response.output_item.added":
			// May announce a new function_call item; capture name + ids.
			if len(env.Item) == 0 {
				continue
			}
			var it struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			}
			if err := json.Unmarshal(env.Item, &it); err != nil {
				continue
			}
			if it.Type == "function_call" {
				key := it.ID
				if key == "" {
					key = it.CallID
				}
				p := tools[key]
				if p == nil {
					p = &pending{}
					tools[key] = p
				}
				p.ItemID = it.ID
				if it.CallID != "" {
					p.CallID = it.CallID
				}
				if it.Name != "" {
					p.Name = it.Name
				}
			}

		case "response.function_call_arguments.delta":
			key := env.ItemID
			if key == "" {
				continue
			}
			p := tools[key]
			if p == nil {
				p = &pending{ItemID: key}
				tools[key] = p
			}
			p.Args.WriteString(env.Delta)

		case "response.output_item.done":
			// Finalize any function_call described in the item payload. Some
			// servers emit call_id only here; this is the last chance to
			// reconcile and emit the tool call.
			if len(env.Item) == 0 {
				continue
			}
			var it struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(env.Item, &it); err != nil {
				continue
			}
			if it.Type != "function_call" {
				continue
			}
			key := it.ID
			if key == "" {
				key = it.CallID
			}
			p := tools[key]
			if p == nil {
				p = &pending{}
				tools[key] = p
			}
			p.ItemID = it.ID
			if it.CallID != "" {
				p.CallID = it.CallID
			}
			if it.Name != "" {
				p.Name = it.Name
			}
			// If the server included a fully-formed Arguments string here and
			// we never saw deltas, use it as the canonical args.
			if p.Args.Len() == 0 && it.Arguments != "" {
				p.Args.WriteString(it.Arguments)
			}
			emitTool(p)
			delete(tools, key)

		case "response.completed":
			u := env.Response.Usage
			if u.TotalTokens > 0 || u.InputTokens > 0 || u.OutputTokens > 0 {
				onEvent(llm.StreamEvent{Usage: &llm.Usage{
					PromptTokens:     u.InputTokens,
					CompletionTokens: u.OutputTokens,
					TotalTokens:      u.TotalTokens,
				}})
			}

		case "response.failed", "response.error", "error":
			msg := env.Error.Message
			if msg == "" {
				msg = "unspecified server error"
			}
			return fmt.Errorf("codex: stream error: %s", msg)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("codex: stream read: %w", err)
	}

	// Flush any tool calls that didn't emit via output_item.done.
	for _, p := range tools {
		emitTool(p)
	}

	onEvent(llm.StreamEvent{Done: true})
	return nil
}
