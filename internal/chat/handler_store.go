package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/store"
)

// StoreHandler implements memory and history built-in tools.
// One struct, five tool names: memory_upsert, memory_list, memory_search,
// history_get, history_search. Each instance handles one tool name.
type StoreHandler struct {
	toolName string
}

func (h StoreHandler) Name() string { return h.toolName }

func (h StoreHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.Store == nil {
		return fmt.Sprintf("error: store not available for tool %s", h.toolName), nil
	}
	switch h.toolName {
	case "memory_upsert":
		return storeMemoryUpsert(string(args), cfg.Store), nil
	case "memory_list":
		return storeMemoryList(string(args), cfg.Store), nil
	case "memory_search":
		return storeMemorySearch(string(args), cfg.Store), nil
	case "history_get":
		return storeHistoryGet(string(args), cfg.Store), nil
	case "history_search":
		return storeHistorySearch(string(args), cfg.Store), nil
	default:
		return fmt.Sprintf("unknown tool: %s", h.toolName), nil
	}
}

func storeMemoryUpsert(rawArgs string, st *store.Store) string {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Core  *bool  `json:"core"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.Key == "" {
		return "error: key required"
	}
	if err := st.MemoryUpsert(args.Key, args.Value, args.Core); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if args.Value == "" {
		return "Removed: " + args.Key
	}
	if args.Core != nil && *args.Core {
		return "Stored (core): " + args.Key
	}
	return "Stored: " + args.Key
}

func storeMemoryList(rawArgs string, st *store.Store) string {
	var args struct {
		CoreOnly bool `json:"core_only"`
		Limit    int  `json:"limit"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	results, err := st.MemoryQuery("", args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemoryEntries(results)
}

func storeMemorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms    []string `json:"terms"`
		Match    string   `json:"match"`
		CoreOnly bool     `json:"core_only"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No memories found."
	}
	results, err := st.MemorySearchExpr(expr, args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemoryEntries(results)
}

func renderMemoryEntries(results []store.MemoryEntry) string {
	if len(results) == 0 {
		return "No memories found."
	}
	var sb strings.Builder
	for _, r := range results {
		marker := ""
		if r.Core {
			marker = " (core)"
		}
		fmt.Fprintf(&sb, "[%s%s]: %s\n", r.Key, marker, r.Value)
	}
	return sb.String()
}

func storeHistoryGet(rawArgs string, st *store.Store) string {
	var args struct {
		SessionID int64 `json:"session_id"`
		Chunk     int   `json:"chunk"`
	}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	chunk := args.Chunk
	if chunk > 0 {
		chunk--
	}
	res, err := st.HistoryGet(args.SessionID, chunk)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.SessionID == 0 && len(res.Turns) == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "session %d, chunk %d/%d (started %s)",
		res.SessionID, res.Chunk+1, res.TotalChunks,
		res.SessionStartedAt.Format("2006-01-02 15:04"))
	if res.PrevSessionID != 0 {
		fmt.Fprintf(&sb, " | prev=%d", res.PrevSessionID)
	}
	if res.NextSessionID != 0 {
		fmt.Fprintf(&sb, " | next=%d", res.NextSessionID)
	}
	sb.WriteByte('\n')
	for _, t := range res.Turns {
		fmt.Fprintf(&sb, "[%s | %s] %s\n",
			t.CreatedAt.Format("2006-01-02 15:04"), t.Role, t.Content)
	}
	return sb.String()
}

func storeHistorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms []string `json:"terms"`
		Match string   `json:"match"`
		Limit int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No history found."
	}
	res, err := st.HistorySearchExpr(expr, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.TotalHits == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "search hits: %d\n", res.TotalHits)
	for _, h := range res.Hits {
		fmt.Fprintf(&sb, "[session %d chunk %d | %s | %s] %s\n",
			h.SessionID, h.Chunk+1,
			h.CreatedAt.Format("2006-01-02 15:04"), h.Role, h.Content)
	}
	return sb.String()
}
