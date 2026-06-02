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

// Info is the session data the UI renders: welcome card / status bar (persona,
// project, current model), the model list + switcher for /model, and the system
// prompt + active tools for /prompt. Model and Switch are callbacks so the
// reported model reflects runtime /model switches.
type Info struct {
	Persona string
	Project string
	Prompt  string
	Tools   []string
	Models  []string
	Model   func() string                     // current model id; nil → ""
	Switch  func(name string) (string, error) // switch active model; nil → switching disabled
}

func (i Info) model() string {
	if i.Model == nil {
		return ""
	}
	return i.Model()
}

// Server exposes a Hub over HTTP: the embedded UI at /, an SSE event stream at
// /events, POST input/cancel/clear/model, and read-only /meta and /prompt.
type Server struct {
	hub  *Hub
	info Info
}

// NewServer wraps a Hub with the session info shown in the UI.
func NewServer(hub *Hub, info Info) *Server { return &Server{hub: hub, info: info} }

// Handler builds the HTTP router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /meta", s.handleMeta)
	mux.HandleFunc("GET /prompt", s.handlePrompt)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /input", s.handleInput)
	mux.HandleFunc("POST /cancel", s.handleCancel)
	mux.HandleFunc("POST /clear", s.handleClear)
	mux.HandleFunc("POST /model", s.handleModel)
	mux.HandleFunc("POST /image", s.handleImage)
	return mux
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"persona": s.info.Persona,
		"project": s.info.Project,
		"model":   s.info.model(),
		"models":  s.info.Models,
	})
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"prompt": s.info.Prompt,
		"tools":  s.info.Tools,
	})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if s.info.Switch == nil {
		http.Error(w, "model switching unavailable", http.StatusBadRequest)
		return
	}
	if s.hub.Busy() {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	id, err := s.info.Switch(strings.TrimSpace(body.Name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"model": id})
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

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if s.hub.Busy() {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	msg, err := chat.BuildImageMessageFromBytes(raw, r.FormValue("prompt"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.hub.SubmitMessage(msg); err != nil {
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
