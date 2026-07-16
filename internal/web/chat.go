//go:build unix

package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SetChat attaches a chat driver, enabling the chat API (/api/send, /api/stop,
// /api/ask) and live state in /api/state. Call before Handler(). Without a
// driver the dashboard stays read-only (the Telegram Mini App and shell3 dash).
func (s *Server) SetChat(d *Driver) { s.chat = d }

// postChat gates a mutating chat endpoint: 404 until a chat driver is
// attached (the read-only dashboards — Telegram Mini App, shell3 dash —
// expose no mutating chat API) and POST-only.
func (s *Server) postChat(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.chat == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

// stateResp is the composer's poll target: capability probe + busy + asks.
type stateResp struct {
	Chat bool  `json:"chat"`
	Busy bool  `json:"busy"`
	Asks []Ask `json:"asks"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	out := stateResp{Asks: []Ask{}}
	if s.chat != nil {
		out.Chat, out.Busy, out.Asks = true, s.chat.Busy(), s.chat.Asks()
	}
	writeJSON(w, out)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		http.Error(w, `bad request: need {"text": ...}`, http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(in.Text)
	// Slash commands run inline and answer synchronously — they never reach
	// the model. Everything else is asynchronous: the driver runs the turn on
	// its own context (a request context dies with this response) and the
	// reply lands in history.
	if strings.HasPrefix(text, "/") {
		writeJSON(w, map[string]string{"reply": s.chat.Command(text)})
		return
	}
	s.chat.Send(text)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.chat.Stop()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" {
		http.Error(w, `bad request: need {"id": ..., "allow": ...}`, http.StatusBadRequest)
		return
	}
	s.chat.Answer(in.ID, in.Allow) // unknown id: harmless no-op
	w.WriteHeader(http.StatusOK)
}
