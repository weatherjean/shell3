package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchmd"
	"github.com/weatherjean/shell3/internal/patchtui"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tui"
)

// LLMClient is the streaming LLM interface.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds all dependencies for a chat session.
type Config struct {
	LLM           LLMClient
	Hooks         *hooks.Runner
	Store         *store.Store
	Personality   persona.Persona
	WorkDir       string
	StatusLine    string
	ModeLabel     string
	Models        []string
	ModelSwitcher func(string)
	Truncate      bool
	Docs          string
}

// appReleaser implements hooks.TTYReleaser by delegating to App.WithReleasedTerminal.
// Hooks call Release() before running and Restore() after; we handle both
// by calling WithReleasedTerminal with a wait-channel pattern.
type appReleaser struct {
	app  *tui.App
	done chan struct{}
}

func (r *appReleaser) Release() error {
	r.done = make(chan struct{})
	go r.app.WithReleasedTerminal(func() { <-r.done })
	return nil
}

func (r *appReleaser) Restore() error {
	if r.done != nil {
		close(r.done)
		r.done = nil
	}
	return nil
}

// RunInteractive runs the TUI chat loop. Blocks until the user quits.
func RunInteractive(ctx context.Context, cfg Config) error {
	sess := &session{}

	var sessionID int64
	if cfg.Store != nil {
		var err error
		sessionID, err = cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("chat: start session: %w", err)
		}
		defer cfg.Store.EndSession(sessionID)
	}

	app := tui.New(cfg.ModeLabel, cfg.StatusLine)

	cfg.Hooks.SetReleaser(&appReleaser{app: app})
	cfg.Hooks.OnSessionStart(ctx)
	defer cfg.Hooks.OnSessionEnd(ctx)

	var lastUsage llm.Usage

	app.SetSubmit(func(input string) {
		trimmed := strings.TrimSpace(input)
		if strings.HasPrefix(trimmed, "/") {
			handleSlash(input, &cfg, sess, app, &lastUsage)
			return
		}
		// Launch turn goroutine.
		ch := make(chan tui.Event, 256)
		turnCtx, cancel := context.WithCancel(ctx)
		app.SetBusy(true, cancel)

		prevLen := len(sess.messages)
		go func() {
			defer cancel()
			runTurn(turnCtx, cfg, sess, input, ch)
			saveHistory(cfg, sess, sessionID, prevLen)
		}()
		go drainTurn(ch, app, &lastUsage, &cfg)
	})

	return app.Run(ctx)
}

// drainTurn consumes events from a turn goroutine, updating App state.
// Streaming text accumulates into a buffer; on TurnDone the buffer is
// committed to scrollback and the App returns to idle.
func drainTurn(ch <-chan tui.Event, app *tui.App, lastUsage *llm.Usage, cfg *Config) {
	var streamBuf strings.Builder
	flushPreview := func() {
		text := streamBuf.String()
		if text == "" {
			app.SetStreamPreview(nil)
			return
		}
		w, _ := patchtui.Size()
		app.SetStreamPreview(patchmd.Render(text, w-2))
	}

	for ev := range ch {
		switch v := ev.(type) {
		case tui.ChunkEvent:
			streamBuf.WriteString(v.Text)
			flushPreview()

		case tui.AppendEvent:
			// Tool output. Commit any pending stream text first so order is preserved.
			if streamBuf.Len() > 0 {
				app.SetStreamPreview(nil)
				w, _ := patchtui.Size()
				app.Print(patchmd.Render(streamBuf.String(), w-2))
				streamBuf.Reset()
			}
			app.Print(splitLines(v.Text))

		case tui.TurnDoneEvent:
			if streamBuf.Len() > 0 {
				app.SetStreamPreview(nil)
				w, _ := patchtui.Size()
				app.Print(patchmd.Render(streamBuf.String(), w-2))
				streamBuf.Reset()
			}
			*lastUsage = v.Usage
			if v.Usage.TotalTokens > 0 {
				app.SetTokens(v.Usage.TotalTokens)
			}
			app.SetBusy(false, nil)

		case tui.TurnErrEvent:
			if streamBuf.Len() > 0 {
				app.SetStreamPreview(nil)
				app.Print(splitLines(streamBuf.String()))
				streamBuf.Reset()
			}
			msg := v.Err.Error()
			if strings.Contains(msg, "context canceled") {
				app.PrintLine(ansiDim + "[cancelled]" + ansiReset)
			} else {
				app.PrintLine("\033[31m[error: " + msg + "]\033[0m")
			}
			app.SetBusy(false, nil)

		case tui.TTYExecEvent:
			// Run the command with full TTY access. The turn goroutine
			// blocks on ReplyC; we deliver the result after the command exits.
			result := "(completed)"
			app.WithReleasedTerminal(func() {
				c := exec.Command("bash", "-c", v.Cmd)
				if v.WorkDir != "" {
					c.Dir = v.WorkDir
				}
				c.Stdin = os.Stdin
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				if err := c.Run(); err != nil {
					result = "error: " + err.Error()
				}
			})
			v.ReplyC <- result
		}
	}
}

// splitLines splits text on '\n', trimming the final empty element if the
// input ends with '\n'.
func splitLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// handleSlash handles /commands. Output goes directly to App.Print.
func handleSlash(input string, cfg *Config, sess *session, app *tui.App, lastUsage *llm.Usage) {
	dim := func(s string) { app.PrintLine(ansiDim + s + ansiReset) }
	defer app.Refresh() // redraw the live frame after slash output
	cmd := strings.TrimSpace(strings.ToLower(input))

	if strings.HasPrefix(cmd, "/model") {
		name := strings.TrimSpace(input[6:])
		if name == "" {
			dim("[/model usage: /model <name>]")
			return
		}
		if cfg.ModelSwitcher != nil {
			cfg.ModelSwitcher(name)
		}
		parts := strings.SplitN(cfg.StatusLine, " │ ", 2)
		provider := ""
		if len(parts) > 0 {
			provider = parts[0]
		}
		cfg.StatusLine = provider + " │ " + name
		app.SetStatus(cfg.StatusLine)
		dim(fmt.Sprintf("[model: %s]", name))
		return
	}

	switch cmd {
	case "/clear":
		sess.messages = nil
		dim("[context cleared]")
	case "/prune":
		pruned := pruneLastTurn(sess.messages)
		if len(pruned) == len(sess.messages) {
			dim("[nothing to prune]")
			return
		}
		sess.messages = pruned
		dim("[last turn removed from context]")
	case "/usage":
		if lastUsage.TotalTokens == 0 {
			dim("[no usage data yet]")
			return
		}
		app.Print([]string{
			fmt.Sprintf("prompt:     %d", lastUsage.PromptTokens),
			fmt.Sprintf("completion: %d", lastUsage.CompletionTokens),
			fmt.Sprintf("total:      %d", lastUsage.TotalTokens),
		})
	case "/prompt":
		var lines []string
		lines = append(lines, ansiBold+"system prompt:"+ansiReset)
		for _, l := range strings.Split(cfg.Personality.SystemPrompt, "\n") {
			lines = append(lines, l)
		}
		lines = append(lines, "", ansiBold+"active tools:"+ansiReset)
		for _, t := range cfg.Personality.Tools {
			lines = append(lines, fmt.Sprintf("  %-16s %s", t.Name, t.Description))
		}
		app.Print(lines)
	case "/truncate":
		cfg.Truncate = !cfg.Truncate
		state := "off"
		if cfg.Truncate {
			state = "on"
		}
		dim(fmt.Sprintf("[full output: %s]", state))
	case "/exit", "/quit":
		os.Exit(0)
	case "/help", "/list", "/", "/h":
		lines := strings.Split(slashHelp(), "\n")
		lines = append(lines, "", "", "", "", "")
		app.Print(lines)
	default:
		dim(fmt.Sprintf("[unknown command: %s  (type /help to list commands)]", input))
	}
}

// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg Config, input string) error {
	sess := &session{}
	ch := make(chan tui.Event, 256)
	go runTurn(ctx, cfg, sess, input, ch)

	for ev := range ch {
		switch v := ev.(type) {
		case tui.ChunkEvent:
			fmt.Print(v.Text)
		case tui.AppendEvent:
			fmt.Print(v.Text)
		case tui.TurnErrEvent:
			fmt.Fprintln(os.Stderr, "error:", v.Err)
		case tui.TurnDoneEvent:
			fmt.Println()
		}
	}
	return nil
}

// pruneLastTurn removes the last user message and everything after it.
func pruneLastTurn(messages []llm.Message) []llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			return messages[:i]
		}
	}
	return messages
}

func slashHelp() string {
	return "\n" + ansiBold + "slash commands:" + ansiReset + "\n" +
		"  /clear     reset conversation context\n" +
		"  /prune     remove last turn from context\n" +
		"  /model     /model <name> to switch\n" +
		"  /usage     show token usage from last turn\n" +
		"  /prompt    dump system prompt and active tools\n" +
		"  /truncate  toggle truncated bash output\n" +
		"  /exit      quit shell3\n" +
		"  /help      show this help\n" +
		"\n" + ansiBold + "keyboard shortcuts:" + ansiReset + "\n" +
		"  enter          send message\n" +
		"  alt+enter      newline in message\n" +
		"  esc            clear input\n" +
		"  ctrl+c         cancel active response\n" +
		"  ctrl+c ctrl+c  quit (when idle)\n" +
		"\n" + ansiBold + "shell passthrough:" + ansiReset + "\n" +
		"  !<cmd>     run shell command with full terminal\n"
}
