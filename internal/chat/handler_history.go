package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/strutil"
)

// HistoryHandler implements the history built-in tool: full-text search over
// the stored conversations, and a read-around view of one session. Read-only
// by construction — it only ever queries the store.
type HistoryHandler struct{}

func (HistoryHandler) Name() string { return "history" }

type historyArgs struct {
	Query   string `json:"query"`
	Session string `json:"session"`
	Around  int    `json:"around"`
	Limit   int    `json:"limit"`
}

// historySnippetCap bounds one rendered message in a transcript excerpt.
const historySnippetCap = 600

func (HistoryHandler) Execute(_ context.Context, _ string, raw json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.Store == nil {
		return "history is unavailable: this session has no store", nil
	}
	var a historyArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "invalid history args: " + err.Error(), nil
	}
	switch {
	case a.Session != "":
		return historyRead(cfg, a), nil
	case strings.TrimSpace(a.Query) != "":
		return historySearch(cfg, a), nil
	default:
		return "history needs a query (search) or a session id (read)", nil
	}
}

func historySearch(cfg ToolConfig, a historyArgs) string {
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}
	hits, err := cfg.Store.Search(a.Query, limit)
	if err != nil {
		// A query that isn't valid FTS5 syntax (stray quote, leading dash)
		// still deserves an answer: retry it as one literal phrase.
		quoted := `"` + strings.ReplaceAll(a.Query, `"`, `""`) + `"`
		if hits, err = cfg.Store.Search(quoted, limit); err != nil {
			return "history search failed: " + err.Error()
		}
	}
	if len(hits) == 0 {
		return "no matches"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es); read context with {\"session\": \"<id>\", \"around\": <seq>}\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "%s #%d %s: %s\n", h.SessionID, h.Seq, h.Role, h.Snippet)
	}
	return strings.TrimRight(b.String(), "\n")
}

func historyRead(cfg ToolConfig, a historyArgs) string {
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	from := 0
	if a.Around > 0 {
		from = max(0, a.Around-limit/2)
	}
	msgs, err := cfg.Store.MessagesRange(a.Session, from, from+limit-1)
	if err != nil {
		return "history read failed: " + err.Error()
	}
	if len(msgs) == 0 {
		return "no messages in that range (unknown session, or seq past the end)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session %s, messages %d–%d\n", a.Session, msgs[0].Seq, msgs[len(msgs)-1].Seq)
	for _, sm := range msgs {
		line := strings.TrimSpace(sm.Msg.Content)
		for _, call := range sm.Msg.ToolCalls {
			if line != "" {
				line += " "
			}
			line += "[tool: " + call.Name + "]"
		}
		if line == "" {
			line = "(empty)"
		}
		fmt.Fprintf(&b, "#%d %s: %s\n", sm.Seq, sm.Msg.Role, strutil.Truncate(line, historySnippetCap))
	}
	return strings.TrimRight(b.String(), "\n")
}
