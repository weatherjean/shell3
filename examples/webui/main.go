// Command webui is a minimal browser front-end over pkg/shell3, for kicking the
// tires on the plugin API shape. It loads your ~/.shell3/shell3.lua config and
// uses the current working directory as the agent's workdir.
//
// One shared Session drives every browser tab; turns are serialized because the
// Session contract requires draining each Send channel before the next Send.
//
//	go run ./examples/webui        # then open http://localhost:8765
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	_ "embed"

	"github.com/weatherjean/shell3/pkg/shell3"
)

//go:embed index.html
var indexHTML []byte

const addr = ":8765"

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".shell3", "shell3.lua")

	sess, err := shell3.Start(context.Background(), shell3.Spec{
		ConfigPath: cfgPath,
		WorkDir:    cwd,
	})
	if err != nil {
		log.Fatalf("start shell3: %v", err)
	}
	defer sess.Close()
	log.Printf("shell3 session %s — config %s — workdir %s", sess.ID(), cfgPath, cwd)

	srv := &server{sess: sess}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	http.HandleFunc("/send", srv.handleSend)

	log.Printf("webui on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

type server struct {
	mu   sync.Mutex // serialize turns: one Send fully drained before the next
	sess *shell3.Session
}

// sseEvent is the JSON shape streamed to the browser per shell3.Event.
type sseEvent struct {
	Kind        string `json:"kind"`
	Text        string `json:"text,omitempty"`
	ToolName    string `json:"toolName,omitempty"`
	ToolInput   string `json:"toolInput,omitempty"`
	ToolOutput  string `json:"toolOutput,omitempty"`
	TotalTokens int    `json:"totalTokens,omitempty"`
	Err         string `json:"err,omitempty"`
}

// handleSend runs one turn for ?msg=... and streams its events as SSE. The
// browser's EventSource only does GET, so the prompt rides in the query string.
func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	msg := r.URL.Query().Get("msg")
	if msg == "" {
		http.Error(w, "missing msg", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// One turn at a time. The mutex is held for the whole drain so the Session
	// contract (drain before next Send) is never violated across tabs.
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := r.Context() // cancelled when the browser disconnects → cancels the turn
	for ev := range s.sess.Send(ctx, msg) {
		// Always drain to completion; just stop writing once the client's gone.
		if ctx.Err() != nil {
			continue
		}
		out := sseEvent{
			Kind:        kindName(ev.Kind),
			Text:        ev.Text,
			ToolName:    ev.ToolName,
			ToolInput:   ev.ToolInput,
			ToolOutput:  ev.ToolOutput,
			TotalTokens: ev.TotalTokens,
		}
		if ev.Err != nil {
			out.Err = ev.Err.Error()
		}
		b, _ := json.Marshal(out)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
}

func kindName(k shell3.Kind) string {
	switch k {
	case shell3.Token:
		return "token"
	case shell3.Reasoning:
		return "reasoning"
	case shell3.ToolCall:
		return "toolCall"
	case shell3.ToolResult:
		return "toolResult"
	case shell3.Usage:
		return "usage"
	case shell3.Retry:
		return "retry"
	case shell3.Error:
		return "error"
	case shell3.Done:
		return "done"
	default:
		return "unknown"
	}
}
