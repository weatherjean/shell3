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
	// Runs lists recent sessions instead of searching text. Agent narrows the
	// listing to one agent's runs. This is the entry point for auditing an
	// employee: list its runs, then read one and see which tools it called.
	Runs  bool   `json:"runs"`
	Agent string `json:"agent"`
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
	case a.Runs || a.Agent != "":
		return historyRuns(cfg, a), nil
	case a.Session != "":
		return historyRead(cfg, a), nil
	case strings.TrimSpace(a.Query) != "":
		return historySearch(cfg, a), nil
	default:
		return "history needs a query (search) or a session id (read)", nil
	}
}

// historyRuns lists recent sessions, newest first, optionally filtered to one
// agent. Auditing an employee starts here: you cannot review a transcript you
// cannot find, and a summary is not evidence of how the work was actually done.
func historyRuns(cfg ToolConfig, a historyArgs) string {
	limit := a.Limit
	if limit <= 0 {
		limit = 15
	}
	metas, err := cfg.Store.ListSessions(0)
	if err != nil {
		return "history runs failed: " + err.Error()
	}
	var b strings.Builder
	n := 0
	for _, m := range metas {
		if a.Agent != "" && !strings.EqualFold(m.Agent, a.Agent) {
			continue
		}
		if n == 0 {
			b.WriteString("recent runs (read one with {\"session\": \"<id>\"}):\n")
		}
		agent := m.Agent
		if agent == "" {
			agent = "-"
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s\n", m.ID, m.StartedAt.Format("2006-01-02 15:04"), agent, m.Status)
		n++
		if n >= limit {
			break
		}
	}
	if n == 0 {
		if a.Agent != "" {
			return "no runs for agent " + a.Agent
		}
		return "no runs"
	}
	return strings.TrimRight(b.String(), "\n")
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
