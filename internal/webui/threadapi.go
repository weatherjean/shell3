//go:build unix

package webui

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The conversation list and its history. The browser holds no durable state:
// it lists threads here, and asks for a thread's messages when it opens one,
// so a reload — or a different device — picks up exactly where things were.

type threadResp struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Preview names an unrenamed thread: the opening of its first message.
	Preview string `json:"preview"`
	Created string `json:"createdAt"`
	Updated string `json:"updatedAt"`
}

func toThreadResp(rec threadRecord) threadResp {
	return threadResp{
		ID: rec.ID, Title: rec.Title, Preview: rec.Preview,
		Created: rec.Created, Updated: rec.Updated,
	}
}

// handleThreads lists conversations, newest activity first.
func (s *Server) handleThreads(w http.ResponseWriter, _ *http.Request) {
	records := s.threads.list()
	out := make([]threadResp, 0, len(records))
	for _, rec := range records {
		out = append(out, toThreadResp(rec))
	}
	writeJSON(w, map[string]any{"threads": out})
}

// handleThread routes the per-thread operations:
//
//	POST   /api/threads/{id}           rename
//	DELETE /api/threads/{id}           forget the thread
//	GET    /api/threads/{id}/messages  the conversation so far
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/threads/")
	id, tail, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "missing thread id", http.StatusBadRequest)
		return
	}

	if tail == "messages" {
		s.handleThreadMessages(w, id)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handleThreadUpdate(w, r, id)
	case http.MethodDelete:
		if !s.threads.remove(id) {
			http.Error(w, "no such thread", http.StatusNotFound)
			return
		}
		s.retire(id)
		writeJSON(w, map[string]bool{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleThreadUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(body.Title)
	if len(title) > 120 {
		title = title[:120]
	}

	rec, ok := s.threads.rename(id, title)
	if !ok {
		http.Error(w, "no such thread", http.StatusNotFound)
		return
	}
	writeJSON(w, toThreadResp(rec))
}

// retire closes a thread's live session, if it has one and nothing is running
// in it. The durable history is untouched: reopening the thread resumes it.
func (s *Server) retire(threadID string) {
	s.mu.Lock()
	sess, live := s.live[threadID]
	if live && (sess == s.turnSession || hasRunningJobs(sess)) {
		s.mu.Unlock()
		return
	}
	if live {
		delete(s.live, threadID)
		kept := s.order[:0]
		for _, id := range s.order {
			if id != threadID {
				kept = append(kept, id)
			}
		}
		s.order = kept
		if s.recentSess == sess {
			s.recentSess = nil
		}
	}
	s.mu.Unlock()

	if live {
		_ = sess.Close()
	}
}

// uiMessage is the AI SDK message shape the browser hydrates a thread from.
type uiMessage struct {
	ID    string   `json:"id"`
	Role  string   `json:"role"`
	Parts []uiPart `json:"parts"`
}

type uiPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// State marks a part finished, so the client renders a closed block rather
	// than one still streaming or a tool still running.
	State string `json:"state,omitempty"`

	// Tool parts. shell3's tools are not declared to the client, so they
	// replay as dynamic tools.
	ToolName   string `json:"toolName,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Input      any    `json:"input,omitempty"`
	Output     any    `json:"output,omitempty"`
}

// handleThreadMessages returns a thread's conversation in the browser's shape:
// what was said, what the model was thinking, and the tools it ran.
//
// Everything the chat showed while it happened is replayed, because a reload
// that quietly drops half of it makes the conversation read differently than
// it did. Tool calls come back already resolved — their results are attached —
// so nothing looks like work still in flight.
func (s *Server) handleThreadMessages(w http.ResponseWriter, threadID string) {
	out := []uiMessage{}

	sessionID, ok := s.threads.lookup(threadID)
	if !ok {
		writeJSON(w, map[string]any{"messages": out})
		return
	}

	history, err := s.rt.SessionMessages(sessionID)
	if err != nil {
		// A swept or missing session is an empty thread, not an error: the
		// user can keep talking in it and a new session is created.
		s.log.Error("webui: could not read thread history", err)
		writeJSON(w, map[string]any{"messages": out})
		return
	}

	// A tool's result is stored as its own entry; fold each one back into the
	// call it answers so the replayed part arrives complete.
	results := map[string]string{}
	for _, entry := range history {
		if entry.Role == "tool" && entry.ToolCallID != "" {
			results[entry.ToolCallID] = entry.Content
		}
	}

	for i, entry := range history {
		if entry.Role != "user" && entry.Role != "assistant" {
			continue
		}

		var parts []uiPart
		if reasoning := strings.TrimSpace(entry.Reasoning); reasoning != "" {
			parts = append(parts, uiPart{
				Type: "reasoning", Text: reasoning, State: "done",
			})
		}
		for _, call := range entry.ToolCalls {
			parts = append(parts, uiPart{
				Type:       "dynamic-tool",
				ToolName:   call.Name,
				ToolCallID: call.ID,
				State:      "output-available",
				Input:      decodeArgs(call.Args),
				Output:     results[call.ID],
			})
		}
		if text := strings.TrimSpace(entry.Content); text != "" {
			parts = append(parts, uiPart{Type: "text", Text: text})
		}
		if len(parts) == 0 {
			continue
		}

		out = append(out, uiMessage{
			ID:    threadID + "-" + itoa(i),
			Role:  entry.Role,
			Parts: parts,
		})
	}
	writeJSON(w, map[string]any{"messages": out})
}

// decodeArgs returns tool arguments as a JSON value so the client can render
// them structured, falling back to the raw string when they are not JSON.
func decodeArgs(args string) any {
	if args == "" {
		return map[string]any{}
	}
	var decoded any
	if json.Unmarshal([]byte(args), &decoded) != nil {
		return args
	}
	return decoded
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
