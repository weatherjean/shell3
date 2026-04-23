package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

// MemorySearchTool searches the SQLite memory store by full-text query.
type MemorySearchTool struct{ db *store.Store }

func NewMemorySearchTool(db *store.Store) *MemorySearchTool { return &MemorySearchTool{db} }

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

func (t *MemorySearchTool) Execute(_ context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.MemorySearch(q, 5)
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
type MemoryStoreTool struct{ db *store.Store }

func NewMemoryStoreTool(db *store.Store) *MemoryStoreTool { return &MemoryStoreTool{db} }

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

func (t *MemoryStoreTool) Execute(_ context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	value, _ := params["value"].(string)
	if err := t.db.MemoryStore(key, value); err != nil {
		return "", fmt.Errorf("memory_store: %w", err)
	}
	return fmt.Sprintf("Stored: %s", key), nil
}

// MemoryRemoveTool removes a key-value entry from the memory store.
type MemoryRemoveTool struct{ db *store.Store }

func NewMemoryRemoveTool(db *store.Store) *MemoryRemoveTool { return &MemoryRemoveTool{db} }

func (t *MemoryRemoveTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_remove",
		Description: "Remove a key-value entry from project memory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key to remove"},
			},
			"required": []string{"key"},
		},
	}
}

func (t *MemoryRemoveTool) Execute(_ context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	if err := t.db.MemoryDelete(key); err != nil {
		return "", fmt.Errorf("memory_remove: %w", err)
	}
	return fmt.Sprintf("Removed: %s", key), nil
}

// MemoryListTool lists all stored memory entries.
type MemoryListTool struct{ db *store.Store }

func NewMemoryListTool(db *store.Store) *MemoryListTool { return &MemoryListTool{db} }

func (t *MemoryListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_list",
		Description: "List all stored memory entries.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *MemoryListTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	results, err := t.db.MemoryList(50)
	if err != nil {
		return "", fmt.Errorf("memory_list: %w", err)
	}
	if len(results) == 0 {
		return "No memories stored.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
	}
	return sb.String(), nil
}

// HistoryLatestTool returns the most recent conversation turns.
type HistoryLatestTool struct{ db *store.Store }

func NewHistoryLatestTool(db *store.Store) *HistoryLatestTool { return &HistoryLatestTool{db} }

func (t *HistoryLatestTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "history_latest",
		Description: "Return the most recent conversation turns across all sessions.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *HistoryLatestTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	results, err := t.db.HistoryLatest(20)
	if err != nil {
		return "", fmt.Errorf("history_latest: %w", err)
	}
	if len(results) == 0 {
		return "No history found.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
			r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
	}
	return sb.String(), nil
}

// HistorySearchTool searches past conversation history by full-text query.
type HistorySearchTool struct{ db *store.Store }

func NewHistorySearchTool(db *store.Store) *HistorySearchTool { return &HistorySearchTool{db} }

func (t *HistorySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "history_search",
		Description: "Search past conversation history for relevant context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *HistorySearchTool) Execute(_ context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.SearchHistory(q, 5)
	if err != nil {
		return "", fmt.Errorf("history_search: %w", err)
	}
	if len(results) == 0 {
		return "No history found.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
			r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
	}
	return sb.String(), nil
}
