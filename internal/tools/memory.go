package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/memory"
)

// MemorySearchTool searches the SQLite memory store by full-text query.
type MemorySearchTool struct{ db *memory.DB }

// NewMemorySearchTool returns a MemorySearchTool backed by db.
func NewMemorySearchTool(db *memory.DB) *MemorySearchTool { return &MemorySearchTool{db} }

// Definition returns the LLM tool definition for memory_search.
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

// Execute searches memory and returns matching entries as formatted text.
func (t *MemorySearchTool) Execute(_ context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.Search(q)
	if err != nil {
		return "", fmt.Errorf("memory_search: %w", err)
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

// MemoryStoreTool stores a key-value entry in the SQLite memory store.
type MemoryStoreTool struct{ db *memory.DB }

// NewMemoryStoreTool returns a MemoryStoreTool backed by db.
func NewMemoryStoreTool(db *memory.DB) *MemoryStoreTool { return &MemoryStoreTool{db} }

// Definition returns the LLM tool definition for memory_store.
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

// Execute stores key/value in memory and returns a confirmation string.
func (t *MemoryStoreTool) Execute(_ context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	value, _ := params["value"].(string)
	if err := t.db.Store(key, value); err != nil {
		return "", fmt.Errorf("memory_store: %w", err)
	}
	return fmt.Sprintf("Stored: %s", key), nil
}
