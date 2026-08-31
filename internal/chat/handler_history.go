package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
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
	Runs   bool   `json:"runs"`
	Agent  string `json:"agent"`
	Cron   string `json:"cron"`
	Parent string `json:"parent"`
	Since  string `json:"since"`
	Before string `json:"before"`
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
	if a.Session != "" {
		return historyRead(cfg, a), nil
	}
	filter, err := historyFilter(a)
	if err != nil {
		return "invalid history filter: " + err.Error(), nil
	}
	if strings.TrimSpace(a.Query) != "" {
		return historySearch(cfg, a, filter), nil
	}
	if a.Runs || hasHistoryFilter(a) {
		return historyRuns(cfg, a, filter), nil
	}
	return "history needs a query (search), runs/filters (list), or a session id (read)", nil
}

func hasHistoryFilter(a historyArgs) bool {
	return a.Agent != "" || a.Cron != "" || a.Parent != "" || a.Since != "" || a.Before != ""
}

func historyFilter(a historyArgs) (runs.SearchFilter, error) {
	f := runs.SearchFilter{Agent: a.Agent, CronJob: a.Cron, ParentID: a.Parent}
	var err error
	if f.Since, err = parseHistoryTime("since", a.Since); err != nil {
		return runs.SearchFilter{}, err
	}
	if f.Before, err = parseHistoryTime("before", a.Before); err != nil {
		return runs.SearchFilter{}, err
	}
	if !f.Since.IsZero() && !f.Before.IsZero() && !f.Since.Before(f.Before) {
		return runs.SearchFilter{}, fmt.Errorf("since must be earlier than before")
	}
	return f, nil
}

func parseHistoryTime(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%s must be YYYY-MM-DD or RFC3339", name)
}

// historyRuns lists recent sessions, newest first, optionally filtered to one
// agent. Auditing an employee starts here: you cannot review a transcript you
// cannot find, and a summary is not evidence of how the work was actually done.
func historyRuns(cfg ToolConfig, a historyArgs, filter runs.SearchFilter) string {
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
		if !historyMetaMatches(m, filter) {
			continue
		}
		if n == 0 {
			b.WriteString("recent runs (read one with {\"session\": \"<id>\"}):\n")
		}
		prompt := ""
		if msgs, loadErr := cfg.Store.LoadMessages(m.ID); loadErr == nil {
			for _, msg := range msgs {
				if msg.Role == llm.RoleUser && strings.TrimSpace(msg.Content) != "" {
					prompt = strutil.Truncate(strings.Join(strings.Fields(msg.Content), " "), 120)
					break
				}
			}
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s", m.ID, m.StartedAt.Format("2006-01-02 15:04"), historyMetaLabel(m.Agent, m.CronJob, m.ParentID), m.Status)
		if prompt != "" {
			fmt.Fprintf(&b, "  %s", prompt)
		}
		b.WriteByte('\n')
		n++
		if n >= limit {
			break
		}
	}
	if n == 0 {
		return "no runs matching those filters"
	}
	return strings.TrimRight(b.String(), "\n")
}

func historyMetaMatches(m runs.Meta, f runs.SearchFilter) bool {
	return (f.Agent == "" || strings.EqualFold(m.Agent, f.Agent)) &&
		(f.CronJob == "" || m.CronJob == f.CronJob) &&
		(f.ParentID == "" || m.ParentID == f.ParentID) &&
		(f.Since.IsZero() || !m.StartedAt.Before(f.Since)) &&
		(f.Before.IsZero() || m.StartedAt.Before(f.Before))
}

func historyMetaLabel(agent, cron, parent string) string {
	parts := []string{"agent=" + valueOrDash(agent)}
	if cron != "" {
		parts = append(parts, "cron="+cron)
	}
	if parent != "" {
		parts = append(parts, "parent="+parent)
	}
	return strings.Join(parts, " ")
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func historySearch(cfg ToolConfig, a historyArgs, filter runs.SearchFilter) string {
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}
	hits, err := cfg.Store.SearchFiltered(a.Query, filter, limit)
	if err != nil {
		// A query that isn't valid FTS5 syntax (stray quote, leading dash)
		// still deserves an answer: retry it as one literal phrase.
		quoted := `"` + strings.ReplaceAll(a.Query, `"`, `""`) + `"`
		if hits, err = cfg.Store.SearchFiltered(quoted, filter, limit); err != nil {
			return "history search failed: " + err.Error()
		}
	}
	if len(hits) == 0 {
		return "no matches"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es); read context with {\"session\": \"<id>\", \"around\": <seq>}\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&b, "%s %s %s #%d %s: %s\n", h.SessionID, h.StartedAt.Format("2006-01-02"),
			historyMetaLabel(h.Agent, h.CronJob, h.ParentID), h.Seq, h.Role, h.Snippet)
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
