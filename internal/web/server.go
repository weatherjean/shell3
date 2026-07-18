//go:build unix

package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// CronJob is the dashboard DTO for one scheduled job and its last run — an
// alias for cron.JobStatus (whose json tags define the /api/cron shape), so
// the host wires cron.Scheduler.Jobs straight in with no field copier.
type CronJob = cron.JobStatus

// HeartbeatStatus is the dashboard DTO for the shell3.heartbeat{} block, shown
// in the Status view. Armed reports whether the running front-end actually
// ticks it (shell3 telegram does; shell3 web and shell3 dash do not).
type HeartbeatStatus struct {
	Every      string `json:"every"`
	Checklist  string `json:"checklist"`
	ActiveFrom string `json:"active_from,omitempty"`
	ActiveTo   string `json:"active_to,omitempty"`
	TZ         string `json:"tz,omitempty"`
	Armed      bool   `json:"armed"`
}

// HeartbeatFromConfig maps the parsed shell3.heartbeat{} block onto the
// dashboard DTO; nil in, nil out (no block declared).
func HeartbeatFromConfig(hb *shell3.Heartbeat, armed bool) *HeartbeatStatus {
	if hb == nil {
		return nil
	}
	return &HeartbeatStatus{
		Every:      hb.Every.String(),
		Checklist:  hb.Checklist,
		ActiveFrom: hb.ActiveFrom,
		ActiveTo:   hb.ActiveTo,
		TZ:         hb.TZ,
		Armed:      armed,
	}
}

// Server is the dashboard (and, with SetChat, the chat API). Auth is
// pluggable: TelegramAuth under the bot, TokenAuth under shell3 web, NoAuth
// under shell3 dash.
type Server struct {
	sess      *shell3.Session
	rt        *shell3.Runtime // used for the past-runs (session store) views
	auth      AuthFunc
	usage     *UsageStore             // nil → no usage shown
	cron      func() []CronJob        // nil → no jobs
	heartbeat func() *HeartbeatStatus // nil → no heartbeat shown
	configDir string                  // root for the read-only file explorer; "" → disabled
	chat      *Driver                 // nil → read-only dashboard (no chat API)
}

func NewServer(rt *shell3.Runtime, sess *shell3.Session, auth AuthFunc) *Server {
	return &Server{rt: rt, sess: sess, auth: auth}
}

// SetUsage attaches a usage store so /api/status reports the last turn's tokens.
func (s *Server) SetUsage(u *UsageStore) { s.usage = u }

// SetCronSource attaches a provider of cron job statuses for /api/cron.
func (s *Server) SetCronSource(fn func() []CronJob) { s.cron = fn }

// SetHeartbeatSource attaches a provider of the heartbeat status for
// /api/status. Re-read on every request, so a source closing over the live
// runtime config picks up a /reload with no re-wiring.
func (s *Server) SetHeartbeatSource(fn func() *HeartbeatStatus) { s.heartbeat = fn }

// SetConfigDir roots the read-only file explorer at dir (the directory holding
// the active shell3.lua). When unset, the file endpoints return empty listings.
func (s *Server) SetConfigDir(dir string) { s.configDir = dir }

func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	var out []CronJob
	if s.cron != nil {
		out = s.cron()
	}
	if out == nil {
		out = []CronJob{}
	}
	writeJSON(w, out)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/history", s.gated(s.handleHistory))
	mux.HandleFunc("/api/jobs", s.gated(s.handleJobs))
	mux.HandleFunc("/api/job", s.gated(s.handleJob))
	mux.HandleFunc("/api/status", s.gated(s.handleStatus))
	mux.HandleFunc("/api/sessions", s.gated(s.handleSessions))
	mux.HandleFunc("/api/session", s.gated(s.handleSession))
	mux.HandleFunc("/api/cron", s.gated(s.handleCron))
	mux.HandleFunc("/api/files", s.gated(s.handleFiles))
	mux.HandleFunc("/api/file", s.gated(s.handleFile))
	// Chat: /api/state is always served (capability probe: chat=false without
	// a driver); the mutating endpoints are POST-only and 404 when no driver
	// is attached. Registered as plain paths, not "POST /path" patterns: the
	// catch-all "/" index route would swallow other methods with a 200 before
	// the mux's built-in 405 could kick in.
	mux.HandleFunc("/api/state", s.gated(s.handleState))
	mux.HandleFunc("/api/send", s.gated(s.postChat(s.handleSend)))
	mux.HandleFunc("/api/stop", s.gated(s.postChat(s.handleStop)))
	mux.HandleFunc("/api/ask", s.gated(s.postChat(s.handleAsk)))
	// Vendored frontend assets (highlight.js + themes). Public, like the inline
	// index — they carry no secrets and the file APIs above remain auth-gated.
	if sub, err := fs.Sub(staticFS, "static"); err == nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	}
	return mux
}

// gated wraps an endpoint with the server's AuthFunc.
func (s *Server) gated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil || !s.auth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type toolCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

// writeJSON sets the JSON Content-Type and encodes v — the tail of every API
// handler.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type historyEntry struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	Reasoning  string     `json:"reasoning,omitempty"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, toEntries(s.sess.History()))
}

// toEntries maps runtime history entries onto the dashboard DTO. Shared by
// handleHistory (live session) and handleSession (stored replay).
func toEntries(hist []shell3.HistoryEntry) []historyEntry {
	out := make([]historyEntry, len(hist))
	for i, h := range hist {
		e := historyEntry{Role: h.Role, Content: h.Content, ToolName: h.ToolName, ToolCallID: h.ToolCallID, Reasoning: h.Reasoning}
		for _, c := range h.ToolCalls {
			e.ToolCalls = append(e.ToolCalls, toolCall{ID: c.ID, Name: c.Name, Args: c.Args})
		}
		out[i] = e
	}
	return out
}

// jobResp is one background job (subagent or command) for the Jobs tab.
type jobResp struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // "subagent" | "command"
	Agent   string `json:"agent,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
	Started string `json:"started"`
	Done    bool   `json:"done"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleJobs lists the session's background jobs (running + finished) —
// subagents are in-process jobs now, so this replaces the old on-disk
// subagent-transcript listing. handleJob (?id=) returns one transcript.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	out := []jobResp{}
	for _, j := range s.sess.Jobs() {
		kind := "command"
		if j.Kind == shell3.JobSubagent {
			kind = "subagent"
		}
		out = append(out, jobResp{
			ID: j.ID, Kind: kind, Agent: j.Agent, Cmd: j.Cmd,
			Started: j.StartedAt.Format(time.RFC3339),
			Done:    j.Done, Summary: j.Summary, Error: j.Error,
		})
	}
	writeJSON(w, out)
}

type statusResp struct {
	Agent         string     `json:"agent"`
	Model         string     `json:"model"`
	ContextWindow int        `json:"context_window"`
	Tools         []string   `json:"tools"`
	Skills        []string   `json:"skills"`
	Subagents     []string   `json:"subagents"`
	Params        []param    `json:"params"`
	SystemPrompt  string     `json:"system_prompt,omitempty"`
	Usage         *usageResp `json:"usage,omitempty"`

	Heartbeat *HeartbeatStatus `json:"heartbeat,omitempty"`
	MCP       []mcpStatus      `json:"mcp,omitempty"`
}

type mcpStatus struct {
	Name      string `json:"name"`
	Up        bool   `json:"up"`
	ToolCount int    `json:"tool_count"`
	Err       string `json:"err,omitempty"`
}

type usageResp struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

type param struct {
	Name    string   `json:"name"`
	Value   string   `json:"value"`
	Default string   `json:"default,omitempty"`
	Enum    []string `json:"enum,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.sess.Snapshot()
	out := statusResp{
		Agent:         snap.Agent,
		Model:         snap.Model,
		ContextWindow: snap.ContextWindow,
		Skills:        snap.Skills,
		Subagents:     snap.Subagents,
		SystemPrompt:  snap.SystemPrompt,
	}
	for _, t := range snap.Tools {
		out.Tools = append(out.Tools, t.Name)
	}
	for _, p := range snap.Params {
		out.Params = append(out.Params, param{Name: p.Name, Value: p.Value, Default: p.Default, Enum: p.Enum})
	}
	if s.usage != nil {
		if p, c, t, ok := s.usage.snapshot(); ok {
			out.Usage = &usageResp{Prompt: p, Completion: c, Total: t}
		}
	}
	if s.heartbeat != nil {
		out.Heartbeat = s.heartbeat()
	}
	for _, m := range snap.MCP {
		out.MCP = append(out.MCP, mcpStatus{Name: m.Name, Up: m.Up, ToolCount: m.ToolCount, Err: m.Err})
	}
	writeJSON(w, out)
}

// handleJob returns one job's raw transcript text (?id=<subN|bgN>): the child
// session's JSONL for subagents, the captured output ring for commands.
func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	transcript := s.sess.JobTranscript(id)
	if transcript == "" {
		transcript = s.sess.JobOutput(id)
	}
	writeJSON(w, map[string]string{"id": id, "transcript": transcript})
}

// handleSessions lists past stored conversations (newest first).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sess, err := s.rt.PastSessions(50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		sess = []shell3.SessionMeta{}
	}
	writeJSON(w, sess)
}

// handleSession returns one past conversation (?id=<n>) at full fidelity —
// tool calls, tool results, and reasoning — so the Runs replay matches the live
// Chat view. Shares the historyEntry shape with handleHistory.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	msgs, err := s.rt.SessionMessages(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, toEntries(msgs))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}
