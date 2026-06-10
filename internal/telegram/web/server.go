//go:build unix

package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Server is the read-only dashboard.
type Server struct {
	sess     *shell3.Session
	rt       *shell3.Runtime // retained for future SSE fan-out
	token    string
	chatID   int64
	usage    *UsageStore                         // nil → no usage shown
	validate func(initData string) (int64, bool) // seam for tests
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

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/history", s.auth(s.handleHistory))
	mux.HandleFunc("/api/subagents", s.auth(s.handleSubagents))
	mux.HandleFunc("/api/subagent", s.auth(s.handleSubagent))
	mux.HandleFunc("/api/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("/api/session", s.auth(s.handleSession))
	mux.HandleFunc("/api/stream", s.auth(s.handleStream))
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
	subs := s.sess.Subagents()
	if subs == nil {
		subs = []shell3.SubagentInfo{}
	}
	_ = json.NewEncoder(w).Encode(subs)
}

type statusResp struct {
	Agent         string     `json:"agent"`
	Model         string     `json:"model"`
	ContextWindow int        `json:"context_window"`
	ProjectRef    string     `json:"project_ref"`
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
		ProjectRef:    snap.ProjectRef,
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

// handleSession returns the turns of one past conversation (?id=<n>).
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	turns, err := s.rt.SessionTurns(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]historyEntry, 0, len(turns))
	for _, t := range turns {
		out = append(out, historyEntry{Role: t.Role, Content: t.Content})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleStream is a v1 poll-based simplification: sends a keep-alive heartbeat
// only. True SSE fan-out (subscribing to rt.Events()) is a follow-up task — the
// bot's consumeWakes is the sole consumer of rt.Events() and sharing the channel
// would race.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML) // Task 10
}
