// Minimal web UI example for the embeddable shell3 chat engine.
//
// Single page, single text box, no streaming. POST /chat runs one turn
// and dumps every event from the session as plain text.
//
// Run:
//
//	go run ./examples/webui
//	# then open http://localhost:8080
package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/shell3"
)

const indexHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>shell3 webui</title>
<style>
body{font-family:system-ui,sans-serif;max-width:780px;margin:2rem auto;padding:0 1rem}
textarea{width:100%%;height:6rem;font:inherit}
pre{background:#111;color:#eee;padding:1rem;white-space:pre-wrap;word-break:break-word}
</style></head><body>
<h1>shell3 webui</h1>
<form method="POST" action="/chat">
<textarea name="msg" autofocus placeholder="say something...">%s</textarea>
<p><button type="submit">Send</button> <em>%s</em></p>
</form>
%s
</body></html>`

type server struct {
	cfg     chat.Config
	handler map[string]chat.ToolHandler
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, indexHTML, "", s.cfg.StatusLine, "")
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	input := strings.TrimSpace(r.FormValue("msg"))
	if input == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:             s.cfg.LLM,
		Personality:     s.cfg.Personality,
		StatusLine:      s.cfg.StatusLine,
		WorkDir:         s.cfg.WorkDir,
		Handlers:        s.handler,
		Log:             chat.LogOrNoop(s.cfg.Log),
		Headless:        true,
		CustomTool:      s.cfg.CustomTool,
		CustomToolNames: s.cfg.CustomToolNames,
		ToolGuard:       s.cfg.ToolGuard,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sess.Run(ctx, tc, input)
		sess.CloseEvents()
	}()

	var dump strings.Builder
	for ev := range sess.Events() {
		fmt.Fprintf(&dump, "[%s]", ev.Kind)
		if ev.Role != "" {
			fmt.Fprintf(&dump, " role=%s", ev.Role)
		}
		if ev.ToolName != "" {
			fmt.Fprintf(&dump, " tool=%s", ev.ToolName)
		}
		if ev.ToolCallID != "" {
			fmt.Fprintf(&dump, " id=%s", ev.ToolCallID)
		}
		if ev.ToolInput != "" {
			fmt.Fprintf(&dump, " input=%s", ev.ToolInput)
		}
		if ev.ToolOutput != "" {
			fmt.Fprintf(&dump, "\n  output: %s", ev.ToolOutput)
		}
		if ev.Text != "" {
			fmt.Fprintf(&dump, "\n  text: %s", ev.Text)
		}
		if ev.Usage != nil {
			fmt.Fprintf(&dump, " prompt=%d completion=%d total=%d",
				ev.Usage.PromptTokens, ev.Usage.CompletionTokens, ev.Usage.TotalTokens)
		}
		dump.WriteString("\n")
	}
	wg.Wait()

	out := "<h2>turn dump</h2><pre>" + html.EscapeString(dump.String()) + "</pre>"
	fmt.Fprintf(w, indexHTML, html.EscapeString(input), s.cfg.StatusLine, out)
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	configPath := flag.String("config", "", "path to shell3.lua (empty = ~/.shell3/shell3.lua)")
	flag.Parse()

	cfg, cleanup, err := shell3.New(shell3.Options{
		ConfigPath: *configPath,
		Headless:   true,
	})
	if err != nil {
		log.Fatalf("bootstrap: %v", err)
	}
	defer cleanup()

	s := &server{cfg: cfg, handler: chat.NewHandlers(cfg)}
	http.HandleFunc("/", s.index)
	http.HandleFunc("/chat", s.chat)

	log.Printf("shell3 webui listening on %s (%s)", *addr, cfg.StatusLine)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
