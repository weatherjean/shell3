package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/store"
)

// StoreHandler implements the history built-in tools.
// One struct, two tool names: history_get, history_search. Each instance
// handles one tool name.
type StoreHandler struct {
	toolName string
}

func (h StoreHandler) Name() string { return h.toolName }

func (h StoreHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.Store == nil {
		return fmt.Sprintf("error: store not available for tool %s", h.toolName), nil
	}
	switch h.toolName {
	case "history_get":
		return storeHistoryGet(string(args), cfg.Store), nil
	case "history_search":
		return storeHistorySearch(string(args), cfg.Store), nil
	default:
		return fmt.Sprintf("unknown tool: %s", h.toolName), nil
	}
}

func storeHistoryGet(rawArgs string, st *store.Store) string {
	var args struct {
		SessionID int64 `json:"session_id"`
		Chunk     int   `json:"chunk"`
	}
	// Empty args is valid here (latest session, first chunk); only a non-empty
	// malformed payload is a tool error. Mirrors the other store handlers.
	if strings.TrimSpace(rawArgs) != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return fmt.Sprintf("error: bad arguments: %v", err)
		}
	}
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
