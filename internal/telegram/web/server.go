//go:build unix

package web

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// CronJob is the dashboard DTO for one scheduled job and its last run. Exported
// so the host (cmd/shell3) can construct it from cron.Scheduler.Jobs().
type CronJob struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Prompt    string `json:"prompt,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
	Notify    bool   `json:"notify"`
	LastRun   string `json:"last_run,omitempty"`
	LastSubID string `json:"last_sub_id,omitempty"`
}

// Server is the read-only dashboard.
type Server struct {
	sess      *shell3.Session
	rt        *shell3.Runtime // used for subagent transcripts and the stream heartbeat
	token     string
	chatID    int64
	usage     *UsageStore                         // nil → no usage shown
	validate  func(initData string) (int64, bool) // seam for tests
	cron      func() []CronJob                    // nil → no jobs
	configDir string                              // root for the read-only file explorer; "" → disabled
}

func NewServer(rt *shell3.Runtime, sess *shell3.Session, token string, chatID int64) *Server {
	s := &Server{rt: rt, sess: sess, token: token, chatID: chatID}
	s.validate = func(initData string) (int64, bool) {
		ok, uid := verifyInitData(initData, s.token, s.chatID)
		return uid, ok
	}
	return s
}

// SetUsage attaches a usage store so /api/status reports the last turn's tokens.
func (s *Server) SetUsage(u *UsageStore) { s.usage = u }

// SetCronSource attaches a provider of cron job statuses for /api/cron.
func (s *Server) SetCronSource(fn func() []CronJob) { s.cron = fn }

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/history", s.auth(s.handleHistory))
	mux.HandleFunc("/api/subagents", s.auth(s.handleSubagents))
	mux.HandleFunc("/api/subagent", s.auth(s.handleSubagent))
	mux.HandleFunc("/api/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("/api/session", s.auth(s.handleSession))
	mux.HandleFunc("/api/cron", s.auth(s.handleCron))
	mux.HandleFunc("/api/files", s.auth(s.handleFiles))
	mux.HandleFunc("/api/file", s.auth(s.handleFile))
	// Vendored frontend assets (highlight.js + themes). Public, like the inline
	// index — they carry no secrets and the file APIs above remain auth-gated.
	if sub, err := fs.Sub(staticFS, "static"); err == nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	}
	return mux
}

// auth gates an endpoint on valid initData (passed as ?initData= or header).
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Init-Data")
		if raw == "" {
			raw = r.URL.Query().Get("initData")
		}
		if _, ok := s.validate(raw); !ok {
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

type historyEntry struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	Reasoning  string     `json:"reasoning,omitempty"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	hist := s.sess.History()
	out := make([]historyEntry, len(hist))
	for i, h := range hist {
		e := historyEntry{Role: h.Role, Content: h.Content, ToolName: h.ToolName, ToolCallID: h.ToolCallID, Reasoning: h.Reasoning}
		for _, c := range h.ToolCalls {
			e.ToolCalls = append(e.ToolCalls, toolCall{ID: c.ID, Name: c.Name, Args: c.Args})
		}
		out[i] = e
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleSubagents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// List subagent transcripts on disk (.shell3_project/agents/*.jsonl). handleSubagent
	// (?id=) returns one transcript's events.
	subs, err := s.rt.SubagentList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if subs == nil {
		subs = []shell3.SubagentInfo{}
	}
	_ = json.NewEncoder(w).Encode(subs)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleSubagent returns one subagent's full transcript (?id=<id>).
func (s *Server) handleSubagent(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	evs, err := s.rt.SubagentTranscript(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if evs == nil {
		evs = []shell3.TranscriptEvent{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(evs)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sess)
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
	out := make([]historyEntry, 0, len(msgs))
	for _, m := range msgs {
		e := historyEntry{Role: m.Role, Content: m.Content, ToolName: m.ToolName, ToolCallID: m.ToolCallID, Reasoning: m.Reasoning}
		for _, c := range m.ToolCalls {
			e.ToolCalls = append(e.ToolCalls, toolCall{ID: c.ID, Name: c.Name, Args: c.Args})
		}
		out = append(out, e)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}
