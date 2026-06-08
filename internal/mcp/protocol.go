package mcp

import (
	"encoding/json"
	"strings"
)

// Spec describes a declared MCP server (stdio transport only).
type Spec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string // optional allowlist; empty means all tools
}

// ToolSchema is one tool exposed by an MCP server.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema
}

// Result is a flattened tools/call result.
type Result struct {
	Text    string
	IsError bool
}

// JSON-RPC 2.0 wire types (internal).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func parseToolsList(raw json.RawMessage) ([]ToolSchema, error) {
	var payload struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := make([]ToolSchema, 0, len(payload.Tools))
	for _, t := range payload.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, ToolSchema{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

func parseCallResult(raw json.RawMessage) (Result, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Result{}, err
	}
	var b strings.Builder
	for _, c := range payload.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	text := b.String()
	if text == "" && len(payload.StructuredContent) > 0 {
		text = string(payload.StructuredContent)
	}
	return Result{Text: text, IsError: payload.IsError}, nil
}
