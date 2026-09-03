// Package console drives the attached orchestrator as a small line-oriented
// conversation. It owns no screen, viewport, or mouse: stdin is input and
// stdout is an append-only transcript. During a turn it temporarily changes
// terminal input mode so Escape can cancel immediately.
package console

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/shell3"
)

const (
	maxSummaryRunes    = 160
	maxBashResultRunes = 240
	maxResultRunes     = 1600
	maxResultLines     = 18
)

var errTurnCancelled = errors.New("console: turn cancelled")

func RunWithReload(ctx context.Context, in io.Reader, out io.Writer, rt *shell3.Runtime, sess *shell3.Session, store inbox.Store, reload func() error) error {
	return run(ctx, in, out, rt, sess, store, reload)
}

func run(ctx context.Context, in io.Reader, out io.Writer, rt *shell3.Runtime, sess *shell3.Session, store inbox.Store, reload func() error) error {
	theme := newTheme(in, out)
	printStartup(out, theme)
	input := newConsoleInput(in)
	inputs, scanErr := input.lines, input.errs
	printInboxStatus(out, theme, store, rt)
	fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-scanErr:
			if !ok {
				scanErr = nil
				continue
			}
			return err
		case line, ok := <-inputs:
			if !ok {
				fmt.Fprintln(out)
				return nil
			}
			line = strings.TrimSpace(line)
			if line == "" {
				fmt.Fprint(out, theme.prompt.Render("you› "))
				continue
			}
			if line == "/quit" || line == "/exit" {
				return nil
			}
			if line == "/" || line == "/h" || line == "/help" {
				printHelp(out, theme)
				fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
				continue
			}
			if line == "/reload" {
				if reload == nil {
					fmt.Fprintln(out, theme.err.Render("reload is unavailable"))
				} else if err := reload(); err != nil {
					fmt.Fprintln(out, theme.err.Render("reload failed: "+err.Error()))
				} else {
					fmt.Fprintln(out, theme.info.Render("config reloaded"))
				}
				fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
				continue
			}
			if line == "/test_output" {
				renderTestOutput(out, theme)
				fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
				continue
			}
			printInboxStatus(out, theme, store, rt)
			if err := runInteractiveTurn(ctx, out, input, func(turnCtx context.Context) <-chan shell3.Event {
				return sess.Send(turnCtx, line)
			}, theme); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			for sess.HasQueuedInput() {
				if err := runInteractiveTurn(ctx, out, input, sess.RunQueued, theme); err != nil && ctx.Err() != nil {
					return ctx.Err()
				}
			}
			fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
		case wake := <-rt.Events():
			if wake.Kind != shell3.Wake || wake.Session != sess.ID() || !sess.HasQueuedInput() {
				continue
			}
			fmt.Fprintln(out, "\n\n"+theme.info.Render("background report"))
			if err := runInteractiveTurn(ctx, out, input, sess.RunQueued, theme); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprint(out, "\n"+theme.prompt.Render("you› "))
		}
	}
}

func printInboxStatus(out io.Writer, theme consoleTheme, store inbox.Store, rt *shell3.Runtime) {
	if store.Root == "" {
		return
	}
	_, count, err := store.List("main", inbox.StatusPending, 0, 1)
	if err != nil {
		rt.Logger().Warn("inbox status failed", "error", err)
		fmt.Fprintln(out, theme.err.Render("inbox status unavailable"))
		return
	}
	if count > 0 {
		fmt.Fprintln(out, theme.info.Render(fmt.Sprintf("inbox · %d pending · ask me to check it", count)))
	}
}

// RunOne renders one non-interactive turn with the same compact transcript.
func RunOne(ctx context.Context, out io.Writer, sess *shell3.Session, prompt string) error {
	return renderEvents(ctx, out, sess.Send(ctx, prompt), nil, nil, newTheme(os.Stdin, out))
}

type turnStarter func(context.Context) <-chan shell3.Event

func runInteractiveTurn(ctx context.Context, out io.Writer, input *consoleInput, start turnStarter, theme consoleTheme) error {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	restore := input.beginTurn()
	defer restore()
	return renderEvents(ctx, out, start(turnCtx), input.cancels, cancel, theme)
}

func renderEvents(ctx context.Context, out io.Writer, events <-chan shell3.Event, cancels <-chan struct{}, cancelTurn context.CancelFunc, theme consoleTheme) error {
	r := turnRenderer{out: out, theme: theme}
	cancelled := false
	r.startThinking()
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-cancels:
			if cancelTurn != nil {
				cancelTurn()
				cancelTurn = nil
				cancels = nil
				cancelled = true
				r.stopThinking()
				fmt.Fprintln(out, theme.info.Render("↳ cancelling turn…"))
			}
		case <-ticker.C:
			r.tickThinking()
		case ev, ok := <-events:
			if !ok {
				r.finish()
				if cancelled {
					return errTurnCancelled
				}
				return r.err
			}
			if cancelled && ev.Kind == shell3.Error && errors.Is(ev.Err, context.Canceled) {
				continue
			}
			r.event(ev)
		}
	}
}

const maxInputBytes = 1 << 20

type consoleInput struct {
	lines   chan string
	cancels chan struct{}
	errs    chan error
	tty     bool
	fd      int
	turn    atomic.Bool
}

func newConsoleInput(in io.Reader) *consoleInput {
	input := &consoleInput{
		lines:   make(chan string),
		cancels: make(chan struct{}, 1),
		errs:    make(chan error, 1),
	}
	if file, ok := in.(*os.File); ok && isTerminal(int(file.Fd())) {
		input.tty = true
		input.fd = int(file.Fd())
	}
	go func() {
		defer close(input.lines)
		defer close(input.errs)
		reader := bufio.NewReader(in)
		line := make([]byte, 0, 256)
		for {
			b, err := reader.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) && len(line) > 0 {
					input.lines <- strings.TrimSuffix(string(line), "\r")
				}
				if !errors.Is(err, io.EOF) {
					input.errs <- err
				}
				return
			}
			if input.tty && input.turn.Load() {
				if b == '\x1b' {
					select {
					case input.cancels <- struct{}{}:
					default:
					}
				}
				continue
			}
			if b == '\n' {
				input.lines <- strings.TrimSuffix(string(line), "\r")
				line = line[:0]
				continue
			}
			if len(line) >= maxInputBytes {
				input.errs <- fmt.Errorf("console input exceeds %d bytes", maxInputBytes)
				return
			}
			line = append(line, b)
		}
	}()
	return input
}

func (i *consoleInput) beginTurn() func() {
	if !i.tty {
		return func() {}
	}
	i.turn.Store(true)
	restore, err := makeInputImmediate(i.fd)
	if err != nil {
		i.turn.Store(false)
		return func() {}
	}
	return func() {
		_ = restore()
		i.turn.Store(false)
	}
}

func printStartup(out io.Writer, theme consoleTheme) {
	fmt.Fprintln(out, theme.dim.Render("Type a request. For example:"))
	fmt.Fprintln(out, theme.dim.Render("  Tell me what you can do"))
	fmt.Fprintln(out, theme.dim.Render("  Build a checked wrk workflow for this task"))
	fmt.Fprintln(out, theme.dim.Render("Commands: /help  /reload  /exit    During a turn: Esc cancels"))
}

func printHelp(out io.Writer, theme consoleTheme) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, theme.info.Render("commands"))
	fmt.Fprintln(out, "  /, /h, /help  show this help")
	fmt.Fprintln(out, "  /reload       validate and reload shell3.lisp")
	fmt.Fprintln(out, "  /exit, /quit  exit shell3")
	fmt.Fprintln(out, "  Esc           cancel the active turn")
}

type turnRenderer struct {
	out      io.Writer
	theme    consoleTheme
	answer   strings.Builder
	thinking bool
	frame    int
	usage    shell3.Event
	err      error
}

func (r *turnRenderer) startThinking() {
	if r.thinking {
		return
	}
	r.thinking = true
	if r.theme.tty {
		fmt.Fprintln(r.out, rainbow("thinking", r.frame))
	} else {
		fmt.Fprintln(r.out, "… thinking")
	}
}

func (r *turnRenderer) tickThinking() {
	if !r.thinking || !r.theme.tty {
		return
	}
	r.frame++
	fmt.Fprint(r.out, "\x1b[1A\r\x1b[2K"+rainbow("thinking", r.frame)+"\n")
}

func (r *turnRenderer) stopThinking() {
	if !r.thinking {
		return
	}
	r.thinking = false
	if r.theme.tty {
		fmt.Fprint(r.out, "\x1b[1A\r\x1b[2K")
	}
}

func (r *turnRenderer) flushAnswer() {
	text := strings.TrimSpace(r.answer.String())
	r.answer.Reset()
	if text == "" {
		return
	}
	r.stopThinking()
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, r.theme.assistant.Render("shell3›"))
	rendered := strings.TrimSpace(cli.RenderMarkdownFor(text, r.theme.tty, r.theme.dark))
	fmt.Fprintln(r.out, rendered)
	fmt.Fprintln(r.out)
}

func (r *turnRenderer) event(ev shell3.Event) {
	switch ev.Kind {
	case shell3.Reasoning:
		r.startThinking()
	case shell3.Token:
		r.startThinking()
		r.answer.WriteString(ev.Text)
	case shell3.ToolCall:
		r.flushAnswer()
		r.stopThinking()
		fmt.Fprintf(r.out, "%s %s\n", r.theme.toolFor(ev.ToolName).Render("→ "+ev.ToolName+":"), r.theme.dim.Render(toolSummary(ev.ToolInput)))
	case shell3.ToolResult:
		r.stopThinking()
		mark := "←"
		style := r.theme.result
		if ev.ToolError {
			mark = "✗"
			style = r.theme.err
		}
		result := truncateResult(ev.ToolName, ev.ToolOutput)
		if isBashTool(ev.ToolName) {
			fmt.Fprintf(r.out, "%s %s\n\n", style.Render(mark+" "+ev.ToolName+":"), r.theme.output.Render(result))
		} else {
			fmt.Fprintf(r.out, "%s\n%s\n\n", style.Render(mark+" "+ev.ToolName), renderLines(r.theme.output, indent(result, "  ")))
		}
		r.startThinking()
	case shell3.Retry:
		r.stopThinking()
		fmt.Fprintln(r.out, r.theme.info.Render("↻ retry: "+compact(ev.Text, maxSummaryRunes)))
		r.startThinking()
	case shell3.Compacted:
		r.stopThinking()
		fmt.Fprintln(r.out, r.theme.dim.Render("◇ context compacted"))
		r.startThinking()
	case shell3.Usage, shell3.Done:
		r.usage = ev
	case shell3.Error:
		r.stopThinking()
		if ev.Err != nil {
			r.err = ev.Err
			fmt.Fprintln(r.out, r.theme.err.Render("error: "+ev.Err.Error()))
		} else {
			r.err = errors.New("turn failed")
			fmt.Fprintln(r.out, r.theme.err.Render("error: turn failed"))
		}
	}
}

func (r *turnRenderer) finish() {
	r.stopThinking()
	r.flushAnswer()
	if r.usage.TotalTokens > 0 {
		fmt.Fprintln(r.out, r.theme.dim.Render(fmt.Sprintf("· %d prompt + %d completion = %d tokens",
			r.usage.PromptTokens, r.usage.CompletionTokens, r.usage.TotalTokens)))
	}
}

func toolSummary(raw string) string {
	var values map[string]any
	if json.Unmarshal([]byte(raw), &values) == nil {
		for _, key := range []string{"command", "file_path"} {
			if value, ok := values[key].(string); ok && value != "" {
				return compact(value, maxSummaryRunes)
			}
		}
	}
	if strings.TrimSpace(raw) == "" {
		return "(no arguments)"
	}
	return compact(strings.Join(strings.Fields(raw), " "), maxSummaryRunes)
}

func truncateResult(tool, s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "(no output)"
	}
	if isBashTool(tool) {
		return compact(s, maxBashResultRunes)
	}
	maxLines, maxRunes := maxResultLines, maxResultRunes
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		tail := 2
		head := maxLines - tail - 1
		hidden := len(lines) - head - tail
		lines = append(append(lines[:head:head], fmt.Sprintf("… %d lines omitted …", hidden)), lines[len(lines)-tail:]...)
		s = strings.Join(lines, "\n")
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	half := (maxRunes - 40) / 2
	return string(runes[:half]) + fmt.Sprintf("\n… %d characters omitted …\n", len(runes)-2*half) + string(runes[len(runes)-half:])
}

func isBashTool(name string) bool {
	return name == "bash" || name == "bash_bg"
}

func renderLines(style lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = style.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

func compact(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-1]) + "…"
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
