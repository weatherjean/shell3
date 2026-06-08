package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchmd"
	"github.com/weatherjean/shell3/internal/patchtui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// session is the slice of *shell3.Session that the interactive loop and slash
// handlers depend on. *shell3.Session satisfies it; tests fake it. Keeping it a
// local interface (rather than taking the concrete *shell3.Session) lets the
// slash-command tests drive each command's effect without standing up a real
// session, agent config, or store — the same pattern the old slashTarget used
// for the App side.
type session interface {
	Send(ctx context.Context, prompt string) <-chan shell3.Event
	SendMessage(ctx context.Context, msg shell3.Message) <-chan shell3.Event
	Clear()
	Rollback() bool
	SwitchAgent(name string) error
	AgentNames() []string
	ActiveAgent() string
	Snapshot() shell3.Snapshot
	History() []shell3.HistoryEntry
	Prune(id string) (summary string, ok bool)
	SetParam(name, value string) error
}

// usage is the TUI-local running tally of the last turn's token counts, fed from
// the public Event token fields on Usage/Done. It replaces the old reliance on
// the internal llm.Usage type so this package imports only pkg/shell3.
type usage struct {
	prompt     int
	completion int
	total      int
}

// RunInteractive runs the TUI chat loop on top of a pkg/shell3 Session. Blocks
// until the user quits.
//
// App-creation ordering: app is declared BEFORE shell3.Start so the
// ShellInteractive closure can capture it, but it is assigned AFTER Start
// returns (using Snapshot() for the welcome/status info). This is safe because
// ShellInteractive only fires during a turn (inside Send), long after app is
// assigned — see pkg/shell3's Spec.ShellInteractive doc.
func RunInteractive(ctx context.Context, spec shell3.Spec) (runErr error) {
	// app is captured by the ShellInteractive closure below but assigned after
	// Start. The closure releases the terminal for an interactive shell command,
	// mirroring the previous in-process interactive-bash runner.
	var app *patchapp.App
	spec.Interactive = true
	spec.ShellInteractive = func(ctx context.Context, cmd, workdir string) string {
		result := "(completed)"
		app.WithReleasedTerminal(func() {
			c := exec.Command("bash", "-c", cmd)
			if workdir != "" {
				c.Dir = workdir
			}
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				result = "error: " + err.Error()
			}
		})
		return result
	}

	sess, err := shell3.Start(ctx, spec)
	if err != nil {
		return err
	}
	// Close cancels any in-flight turn, joins it (so its deferred history persist
	// runs against the still-open store), ends the store session, and flushes the
	// audit log. pkg owns all of that now, so this loop no longer manages the
	// store EndSession defer or the JSONL sink itself.
	defer sess.Close()

	// workDir is captured for /image (Snapshot carries no WorkDir). spec.WorkDir
	// is the source the old code used via cfg.WorkDir; fall back to the cwd when
	// the spec left it empty (matching shell3.Start's own resolution).
	workDir := spec.WorkDir
	if workDir == "" {
		if w, gwErr := os.Getwd(); gwErr == nil {
			workDir = w
		}
	}

	snap := sess.Snapshot()
	app = patchapp.New(snap.Agent, snap.StatusLine, patchapp.WelcomeInfo{
		Persona:    snap.Agent,
		ProjectRef: snap.ProjectRef,
	})
	if snap.ContextWindow > 0 {
		app.SetContextWindow(snap.ContextWindow)
	}

	var lastUsage usage

	// turnWG tracks the in-flight turn goroutine (spawned per user message by
	// launchTurn). It only drains the public Event channel — pkg persists history
	// and ends the store inside Send/Close — but teardown still joins it so the
	// render sink isn't writing to the App while the loop unwinds.
	var turnWG sync.WaitGroup

	// turnsCtx is the parent of every per-turn context. Teardown cancels it so
	// turnWG.Wait() never blocks on a turn that wasn't otherwise cancelled:
	// app.Run can return WITHOUT cancelling the caller's ctx (e.g. stdin EOF), in
	// which case an in-flight turn's per-turn ctx (derived from this one) would
	// not be cancelled and the turn could run on indefinitely. Cancelling
	// turnsCtx at teardown forces a prompt unwind in all exit paths.
	turnsCtx, cancelTurns := context.WithCancel(ctx)
	// safety net; the ordering-critical cancel is in the teardown defer below.
	defer cancelTurns()

	// Teardown: cancel the in-flight turn and join its drain goroutine before
	// sess.Close runs (deferred above, so it runs after this). Once turnWG.Wait()
	// returns, no goroutine can still call the render sink.
	defer func() {
		cancelTurns()
		turnWG.Wait()
	}()

	renderSink, finishTurn := newRenderSink(app, &lastUsage)

	// launchTurn starts a turn goroutine for msg. The render sink runs on that
	// goroutine as it drains the per-turn Event channel; per-turn UI state
	// transitions (SetBusy false, etc.) happen when the sink processes the
	// Done/Error event. pkg persists history inside the turn.
	launchTurn := func(msg shell3.Message) {
		turnCtx, cancel := context.WithCancel(turnsCtx)
		app.SetBusy(true, cancel)
		var ch <-chan shell3.Event
		// A plain-text message drives Send (string path); a message carrying an
		// attachment (e.g. /image) drives SendMessage so its built payload isn't
		// dropped. Both share the per-turn drain/cancel machinery below.
		if len(msg.Attachments) == 0 {
			ch = sess.Send(turnCtx, msg.Text)
		} else {
			ch = sess.SendMessage(turnCtx, msg)
		}
		turnWG.Add(1)
		go func() {
			defer turnWG.Done()
			defer cancel()
			// finishTurn runs at channel close (the guaranteed end-of-turn signal)
			// BEFORE the deferred cancel() above — deferred LIFO — so turnCtx.Err()
			// still reflects only an external cancel (Ctrl-C/ESC), not our own
			// teardown. Deferred so a panic in the sink can't strand the busy-gate.
			defer func() { finishTurn(turnCtx.Err() != nil) }()
			for ev := range ch {
				renderSink(ev)
			}
		}()
	}

	// applyAgent refreshes the badge/status/context-window after a switch. The
	// switch itself already happened on sess (SwitchAgent); we read the fresh
	// Snapshot to mirror it into the App.
	applyAgent := func() {
		snap := sess.Snapshot()
		app.SetMode(snap.Agent)
		app.SetStatus(snap.StatusLine)
		app.SetContextWindow(snap.ContextWindow)
	}

	app.SetTab(func() {
		names := sess.AgentNames()
		if len(names) < 2 {
			return
		}
		cur := 0
		active := sess.ActiveAgent()
		for i, n := range names {
			if n == active {
				cur = i
				break
			}
		}
		next := names[(cur+1)%len(names)]
		if err := sess.SwitchAgent(next); err != nil {
			return
		}
		applyAgent()
		app.PrintLine(patchtui.Dim + "[agent: " + sess.ActiveAgent() + "]" + patchtui.Reset)
	})

	registerSlashCommands(app, sess, &lastUsage, workDir, launchTurn, applyAgent)

	app.SetSubmit(func(input string) {
		launchTurn(shell3.Message{Text: input})
	})

	runErr = app.Run(ctx)
	return
}

// newRenderSink returns the function that renders a stream of public Events to
// the App. LLM text streams to scrollback line-by-line via patchmd (or verbatim
// inside fenced code blocks). Reasoning chunks stream dim, also line-by-line.
// Tool calls render a header line; tool results render a dimmed body (or
// colorized diff for edit_file). Done flushes the trailing partial line and
// clears busy. The returned closure owns the per-turn streaming state (stream/
// reasoning buffers, fence toggle), which is flushed and reset at Done/Error.
//
// The JSONL audit log is no longer written here — pkg/shell3 owns the sink and
// writes every (lossless) internal event before translating to the public Event
// streamed below.
//
// CONCURRENCY INVARIANT (busy-gate): the sink runs on the in-flight turn's drain
// goroutine and WRITES *lastUsage. The slash-command handlers in
// registerSlashCommands run on the input-loop goroutine and READ *lastUsage
// (/usage). There is deliberately NO mutex around lastUsage. This is race-free
// ONLY because of the busy-gate in patchapp: App.handleEnter
// (internal/patchapp/editor.go) early-returns while a.busy is true, so slash
// handlers (and SubmitFunc) cannot fire while a turn — and therefore the sink's
// writes of lastUsage — is active. SetBusy(true) is set in launchTurn for the
// duration of the turn and cleared in finish, which the drain goroutine runs at
// channel close — strictly after every event (including the last lastUsage
// write) has been processed. (Busy is deliberately NOT cleared in the Done/Error
// sink cases: route may drop that terminal event on cancel, which previously
// left busy stuck on; binding the clear to channel close fixes that and also
// guarantees no sink write can follow the clear.) A future maintainer who breaks
// that gate (e.g. allowing slash commands to run during streaming, or clearing
// busy before channel close) reintroduces a data race on lastUsage and must add
// real synchronization here.
func newRenderSink(app patchapp.AppView, lastUsage *usage) (func(shell3.Event), func(canceled bool)) {
	var streamBuf strings.Builder
	// reasoningBuf holds an incomplete (no trailing \n) reasoning line.
	// Complete lines are committed to scrollback immediately for real-time display.
	var reasoningBuf strings.Builder
	reasoningStarted := false
	// terminalRendered records whether this turn's Done/Error event was actually
	// delivered to the sink. route (pkg/shell3) may drop the terminal event once
	// the turn ctx is cancelled, so finish uses this to avoid double-rendering the
	// cancel notice when the event DID arrive, and to know it must render it when
	// the event did NOT. Reset at the end of finish for the next turn.
	terminalRendered := false

	// Sage-green diamond prepended to the first reasoning line of a turn.
	const thinkingGlyph = "\033[38;2;130;195;130m◆\033[0m"

	commitReasoningLine := func(line string) {
		prefix := ""
		if !reasoningStarted {
			prefix = thinkingGlyph + " "
			reasoningStarted = true
		}
		app.Print([]string{prefix + patchtui.MutedThinking + line + patchtui.Reset})
	}

	// flushReasoningPartial commits any buffered partial reasoning line and
	// emits a trailing blank line if any reasoning was shown this turn.
	flushReasoningPartial := func() {
		if reasoningBuf.Len() == 0 && !reasoningStarted {
			return
		}
		if reasoningBuf.Len() > 0 {
			commitReasoningLine(reasoningBuf.String())
			reasoningBuf.Reset()
		}
		if reasoningStarted {
			app.Print([]string{""})
			reasoningStarted = false
		}
	}

	// inFence tracks whether streaming is currently inside a ``` fenced code
	// block so we can emit those lines verbatim (no inline-markdown
	// processing). The fence toggle line itself is printed dim.
	inFence := false

	// flushStream commits complete lines from streamBuf to scrollback,
	// keeping any partial trailing line in the buffer for the next chunk.
	// Lines outside a fenced code block are rendered via patchmd per-line;
	// lines inside a fence are printed verbatim. This is the only path that
	// makes streamed LLM text visible — the live frame during busy is just
	// the status bar (see patchapp.buildFrame).
	flushStream := func() {
		text := streamBuf.String()
		if text == "" {
			return
		}
		w, _ := patchtui.Size()
		var emit []string
		for {
			idx := strings.IndexByte(text, '\n')
			if idx < 0 {
				break
			}
			line := text[:idx]
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "```") {
				inFence = !inFence
				emit = append(emit, patchtui.Dim+line+patchtui.Reset)
			} else if inFence {
				emit = append(emit, line)
			} else {
				emit = append(emit, patchmd.Render(line, w-2)...)
			}
			text = text[idx+1:]
		}
		streamBuf.Reset()
		streamBuf.WriteString(text)
		if len(emit) > 0 {
			app.Print(emit)
		}
	}
	// publishUsage forwards the per-roundtrip token counts to the status bar and
	// records them for /usage. The Event carries the counts directly now (no
	// llm.Usage), so we compare against the running tally to avoid redundant
	// SetTokens calls.
	publishUsage := func(ev shell3.Event) {
		u := usage{prompt: ev.PromptTokens, completion: ev.CompletionTokens, total: ev.TotalTokens}
		if u == *lastUsage {
			return
		}
		*lastUsage = u
		if u.total > 0 {
			app.SetTokens(u.total)
		}
	}
	// appendReasoning commits each complete (newline-terminated) reasoning line
	// to scrollback as it arrives (dim gray), keeping the trailing partial line
	// in reasoningBuf until the next newline or a flush.
	appendReasoning := func(text string) {
		for {
			idx := strings.IndexByte(text, '\n')
			if idx < 0 {
				reasoningBuf.WriteString(text)
				break
			}
			commitReasoningLine(reasoningBuf.String() + text[:idx])
			reasoningBuf.Reset()
			text = text[idx+1:]
		}
	}

	// flushStreamFully commits any partial line as a final block (no trailing
	// newline). Used before tool output or end-of-turn to preserve order.
	flushStreamFully := func() {
		if streamBuf.Len() == 0 {
			return
		}
		w, _ := patchtui.Size()
		app.Print(patchmd.Render(streamBuf.String(), w-2))
		streamBuf.Reset()
	}

	sink := func(ev shell3.Event) {
		switch ev.Kind {
		case shell3.Reasoning:
			appendReasoning(ev.Text)

		case shell3.Token:
			flushReasoningPartial()
			streamBuf.WriteString(ev.Text)
			flushStream()

		case shell3.ToolCall:
			// Render the per-tool header. Body arrives later via ToolResult.
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(renderToolCallHeader(ev) + "\n"))

		case shell3.ToolResult:
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(renderToolResultBody(ev) + "\n\n"))

		case shell3.SystemReminder:
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(patchtui.Dim + ev.Text + patchtui.Reset + "\n\n"))

		case shell3.Retry:
			// A transient failure is being retried (pre-token, so buffers are
			// empty). Render a dim notice and leave busy state untouched — the
			// turn is still in progress.
			app.Print(patchtui.SplitLines(patchtui.Dim + "⟳ " + ev.Text + patchtui.Reset + "\n"))

		case shell3.Usage:
			publishUsage(ev)

		case shell3.Done:
			flushReasoningPartial()
			flushStreamFully()
			publishUsage(ev)
			terminalRendered = true

		case shell3.Error:
			flushReasoningPartial()
			if streamBuf.Len() > 0 {
				app.Print(patchtui.SplitLines(streamBuf.String()))
				streamBuf.Reset()
			}
			msg := ""
			if ev.Err != nil {
				msg = ev.Err.Error()
			}
			if strings.Contains(msg, "context canceled") {
				app.PrintLine(patchtui.Dim + "[cancelled]" + patchtui.Reset)
			} else {
				app.PrintLine(patchtui.Red + "[error: " + msg + "]" + patchtui.Reset)
			}
			terminalRendered = true
		}
	}

	// finish finalizes a turn at channel close — the ONLY guaranteed end-of-turn
	// signal, since route may drop the terminal Done/Error event when the turn
	// ctx is cancelled (see the pkg/shell3 SendMessage/route contract). It flushes
	// any partial output the dropped Done would have flushed, surfaces the cancel
	// notice when the terminal Error was dropped, and clears the busy-gate so the
	// "thinking" spinner always stops. Clearing busy here (rather than in the
	// Done/Error cases) also tightens the busy-gate invariant above: busy stays
	// true until the drain goroutine has processed every event, so no sink write
	// to lastUsage can follow the clear. Idempotent flushes make a redundant call
	// harmless; canceled is the turn ctx's cancellation state at channel close.
	finish := func(canceled bool) {
		flushReasoningPartial()
		flushStreamFully()
		inFence = false
		if canceled && !terminalRendered {
			app.PrintLine(patchtui.Dim + "[cancelled]" + patchtui.Reset)
		}
		app.SetBusy(false, nil)
		terminalRendered = false
	}
	return sink, finish
}

// slashTarget abstracts the side of patchapp.App used by slash command
// handlers. This is what RegisterSlash needs from us; concrete *App
// satisfies it. We don't reuse appView so the registration site can be
// tested without dragging in event-drain machinery.
type slashTarget interface {
	Print(lines []string)
	PrintLine(line string)
	SetStatus(msg string)
	SetContextWindow(n int)
	RegisterSlash(cmd patchapp.SlashCommand)
	WithReleasedTerminal(fn func())
	Quit()
}

// registerSlashCommands wires up the slash registry. Closures capture sess,
// lastUsage, and workDir so handlers can read and mutate session state via the
// public pkg/shell3 API.
//
// These handlers read *lastUsage with NO mutex; that is race-free only because
// of the busy-gate. See newRenderSink for the full CONCURRENCY INVARIANT (this
// is the read side).
func registerSlashCommands(app slashTarget, sess session, lastUsage *usage, workDir string, launchTurn func(shell3.Message), applyAgent func()) {
	dim := func(s string) { app.PrintLine(patchtui.Dim + s + patchtui.Reset) }

	app.RegisterSlash(patchapp.SlashCommand{
		Name: "clear", Help: "reset conversation context",
		Handler: func(string) {
			// Clear drops history and re-stamps the system prompt with a fresh
			// timestamp inside the Session.
			sess.Clear()
			dim("[context cleared]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "rollback", Help: "remove last turn from context",
		Handler: func(string) {
			if !sess.Rollback() {
				dim("[nothing to roll back]")
				return
			}
			dim("[last turn removed from context]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "prune", Help: "/prune <id> — replace tool result <id> with a stub",
		Handler: func(args string) {
			id := strings.TrimSpace(args)
			if id == "" {
				dim("[/prune usage: /prune <tool_call_id>]")
				return
			}
			out, _ := sess.Prune(id)
			dim("[" + out + "]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "usage", Help: "show token usage from last turn",
		Handler: func(string) {
			if lastUsage.total == 0 {
				dim("[no usage data yet]")
				return
			}
			app.Print([]string{
				fmt.Sprintf("prompt:     %d", lastUsage.prompt),
				fmt.Sprintf("completion: %d", lastUsage.completion),
				fmt.Sprintf("total:      %d", lastUsage.total),
			})
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "prompt", Help: "dump system prompt and active tools",
		Handler: func(string) {
			w, _ := patchtui.Size()
			if w < 20 {
				w = 80
			}
			snap := sess.Snapshot()

			lines := []string{
				"",
				patchtui.Yellow + patchtui.Bold + "System prompt" + patchtui.Reset,
				patchtui.Dim + strings.Repeat("─", min(40, max(0, w-2))) + patchtui.Reset,
			}
			prompt := strings.TrimSpace(snap.SystemPrompt)
			if prompt == "" {
				lines = append(lines, patchtui.Dim+"(empty)"+patchtui.Reset)
			} else {
				lines = append(lines, patchmd.Render(prompt, w-2)...)
			}

			lines = append(lines, "", patchtui.Cyan+patchtui.Bold+"Active tools"+patchtui.Reset)
			if len(snap.Tools) == 0 {
				lines = append(lines, "  "+patchtui.Dim+"(none)"+patchtui.Reset)
			} else {
				for _, t := range snap.Tools {
					lines = append(lines,
						"  "+patchtui.Green+patchtui.Bold+t.Name+patchtui.Reset,
						"    "+patchtui.Dim+t.Description+patchtui.Reset,
					)
				}
			}
			lines = append(lines, "")
			app.Print(lines)
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "print", Help: "/print <id> — show the full (untruncated) output of tool result <id>",
		Handler: func(args string) {
			id := strings.TrimSpace(args)
			if id == "" {
				dim("[/print usage: /print <tool_call_id>]")
				return
			}
			// History returns Content already prefix-stripped, so /print can match
			// on ToolCallID and show the raw output directly.
			for _, m := range sess.History() {
				if m.ToolCallID == id && m.Role == "tool" {
					body := strings.TrimRight(m.Content, "\n")
					app.Print(patchtui.SplitLines(dimLines(body) + "\n\n"))
					return
				}
			}
			dim(fmt.Sprintf("[no tool result with id %q]", id))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "parameters",
		Help: "/parameters [name value] — list or set tunable params (reasoning_effort, max_tokens, ...)",
		Handler: func(args string) {
			args = strings.TrimSpace(args)
			if args == "" {
				params := sess.Snapshot().Params
				if len(params) == 0 {
					dim("[current provider exposes no parameters]")
					return
				}
				lines := []string{patchtui.Bold + "parameters:" + patchtui.Reset}
				for _, p := range params {
					cur := p.Value
					if cur == "" {
						cur = "—"
					}
					enum := ""
					if len(p.Enum) > 0 {
						enum = " [" + strings.Join(p.Enum, "|") + "]"
					}
					def := p.Default
					if def == "" {
						def = "provider"
					}
					lines = append(lines, fmt.Sprintf("  %-22s = %-8s%s  (default %s)", p.Name, cur, enum, def))
				}
				lines = append(lines, "", patchtui.Dim+"usage: /parameters <name> <value>"+patchtui.Reset)
				app.Print(lines)
				return
			}
			parts := strings.Fields(args)
			if len(parts) != 2 {
				dim("[usage: /parameters <name> <value>]")
				return
			}
			name, value := parts[0], parts[1]
			if err := sess.SetParam(name, value); err != nil {
				dim(fmt.Sprintf("[%v]", err))
				return
			}
			// SetParam re-derives the status line for reasoning_effort; refresh
			// the bar from the fresh Snapshot regardless (cheap and correct).
			app.SetStatus(sess.Snapshot().StatusLine)
			dim(fmt.Sprintf("[%s = %s]", name, value))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "agent", Help: "/agent [name] — list agents or switch the active agent",
		Handler: func(args string) {
			names := sess.AgentNames()
			if len(names) == 0 {
				dim("[no agents configured]")
				return
			}
			name := strings.TrimSpace(args)
			if name == "" {
				active := sess.ActiveAgent()
				lines := []string{patchtui.Bold + "agents:" + patchtui.Reset}
				for _, n := range names {
					marker := ""
					if n == active {
						marker = patchtui.Dim + "  (active)" + patchtui.Reset
					}
					lines = append(lines, "  "+n+marker)
				}
				lines = append(lines, "", patchtui.Dim+"usage: /agent <name>"+patchtui.Reset)
				app.Print(lines)
				return
			}
			if err := sess.SwitchAgent(name); err != nil {
				dim(fmt.Sprintf("[%v]", err))
				return
			}
			applyAgent()
			dim(fmt.Sprintf("[agent: %s]", sess.ActiveAgent()))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "info", Help: "show session details: agent, project, skills, tools",
		Handler: func(string) {
			snap := sess.Snapshot()
			lines := []string{""}
			add := func(label, value string) {
				if value != "" {
					lines = append(lines, patchtui.Bold+label+patchtui.Reset)
					lines = append(lines, "    "+value)
				}
			}
			add("agent", snap.Agent)
			add("project", snap.ProjectRef)
			if len(snap.Skills) > 0 {
				lines = append(lines, patchtui.Bold+"skills"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(snap.Skills, ", "))
			}
			if len(snap.Tools) > 0 {
				lines = append(lines, patchtui.Bold+"tools"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(toolNames(snap.Tools), ", "))
			}
			lines = append(lines, "")
			app.Print(lines)
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "exit", Aliases: []string{"quit"}, Help: "quit shell3",
		Handler: func(string) { app.Quit() },
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "image", Help: "/image <path> [prompt] — attach image to next turn",
		Handler: func(args string) {
			msg, err := shell3.ImageMessage(args, workDir)
			if err != nil {
				dim(fmt.Sprintf("[image: %v]", err))
				return
			}
			launchTurn(msg)
		},
	})
}

func toolNames(tools []shell3.ToolInfo) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}
