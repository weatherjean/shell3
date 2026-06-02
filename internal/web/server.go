package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/weatherjean/shell3/pkg/chat"
)

// Meta is the static session info the UI renders in its welcome card and
// status bar (persona, model, project ref).
type Meta struct {
	Persona string `json:"persona"`
	Model   string `json:"model"`
	Project string `json:"project"`
}

// Server exposes a Hub over HTTP: the embedded UI at /, an SSE event stream at
// /events, POST endpoints for input/cancel/clear, and session meta at /meta.
type Server struct {
	hub  *Hub
	meta Meta
}

// NewServer wraps a Hub with the session meta shown in the UI.
func NewServer(hub *Hub, meta Meta) *Server { return &Server{hub: hub, meta: meta} }

// Handler builds the HTTP router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /meta", s.handleMeta)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /input", s.handleInput)
	mux.HandleFunc("POST /cancel", s.handleCancel)
	mux.HandleFunc("POST /clear", s.handleClear)
	return mux
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.meta)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	replay, ch, unsub := s.hub.Subscribe()
	defer unsub()
	for _, ev := range replay {
		writeSSE(w, ev)
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, ev chat.Event) {
	b, err := chat.MarshalEventJSON(ev)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		http.Error(w, "empty input", http.StatusBadRequest)
		return
	}
	if text == "/clear" {
		if err := s.hub.Clear(); err != nil {
			http.Error(w, "agent busy", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.hub.Submit(text); err != nil {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.hub.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if err := s.hub.Clear(); err != nil {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
