package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchmd"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// RunInteractive runs the TUI chat loop. Blocks until the user quits.
func RunInteractive(ctx context.Context, cfg chat.Config) (runErr error) {
	var storeID int64
	if cfg.Store != nil {
		var err error
		storeID, err = cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("chat: start session: %w", err)
		}
	}
	sess := chat.NewSession(chat.SessionOpts{
		BufSize:          256,
		StoreID:          storeID,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
	})
	if cfg.Store != nil {
		// End whichever session is current when the loop exits. compact_history
		// may roll sess.id to a new session mid-conversation, so read sess.ID()
		// at defer time rather than capturing the initial storeID.
		lg := chat.LogOrNoop(cfg.Log)
		defer func() {
			if err := cfg.Store.EndSession(sess.ID()); err != nil {
				lg.Warn("end session failed", "session_id", sess.ID(), "error", err)
			}
		}()
	}

	sink, sinkCleanup, openErr := chat.OpenSink(cfg.OutPath)
	if openErr != nil {
		return openErr
	}
	if sink != nil {
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart("(interactive)", cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}
	{
		_, model := chat.SplitStatus(cfg.StatusLine)
		sess.Start(map[string]string{
			"mode":    cfg.ModeLabel,
			"persona": cfg.Personality.Name,
			"model":   model,
			"out":     cfg.OutPath,
		})
	}

	app := patchapp.New(cfg.ModeLabel, cfg.StatusLine, patchapp.WelcomeInfo{
		Persona:      cfg.ModeLabel,
		ProjectRef:   cfg.ProjectRef,
		ActiveSkills: cfg.ActiveSkills,
		ActiveTools:  cfg.ActiveTools,
	})
	if cfg.ContextWindow > 0 {
		app.SetContextWindow(cfg.ContextWindow)
	}

	var lastUsage llm.Usage

	// Long-lived drain: single consumer of sess.Events() for the whole session.
	// drainTurn renders to the app and (if sink != nil) also writes the JSONL
	// audit log. WaitGroup ensures all writes land before sinkCleanup closes
	// the file. emit() recovers from send-on-closed so late hook emissions
	// during teardown don't panic.
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		drainTurn(sess.Events(), app, &lastUsage, &cfg, sink)
	}()

	// turnWG tracks the in-flight turn goroutine (spawned per user message by
	// launchTurn). The turn goroutine writes to cfg.Store (saveHistory/compact)
	// as it unwinds, so teardown must JOIN it before EndSession runs — otherwise
	// a cancelled turn could still be writing to the store while EndSession (and,
	// in the embedded case, a subsequent store Close) runs. See the teardown
	// defer below for the ordering that avoids both the inconsistent interleave
	// and a drain deadlock.
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

	// Ordered teardown: cancel + join the in-flight turn (so its store writes
	// finish) WHILE drainTurn is still consuming events, then emit session_end,
	// close channel, wait for drain, then WriteEnd + sinkCleanup. Single defer
	// enforces ordering.
	//
	// Why cancelTurns()+turnWG.Wait() come before sess.CloseEvents(): the turn
	// goroutine emits events that drainTurn consumes, and drainTurn keeps running
	// until CloseEvents closes sess.Events(). Cancelling turnsCtx makes the turn
	// unwind promptly; leaving drainTurn consuming means the unwinding turn can
	// still flush its final events (and finish its store writes) without blocking
	// on a backed-up channel. If we closed events or stopped the drain first, the
	// turn goroutine could deadlock trying to emit and never return — so
	// turnWG.Wait() MUST happen while drain is still alive.
	defer func() {
		status := "ok"
		if runErr != nil {
			status = "error"
		}
		// Cancel any in-flight turn, then join it — while drain still consumes
		// its events — so no store write is in flight when EndSession runs.
		// cancelTurns is also deferred as the lostcancel safety net; this explicit
		// call is the ordering-critical one — it must run before turnWG.Wait() so
		// in-flight turns unwind before we join them.
		cancelTurns()
		turnWG.Wait()
		sess.End(status)
		sess.CloseEvents()
		drainWG.Wait()
		if sink != nil {
			sink.WriteEnd(status)
		}
		sinkCleanup()
	}()

	handlers := chat.NewHandlers(cfg)

	// launchTurn starts a turn goroutine for userMsg. drainTurn runs as a
	// long-lived consumer of sess.Events() (started above); per-turn UI
	// state transitions (SetBusy false, etc.) happen via TurnDone/Error
	// events. History is persisted inside the turn (before the terminal
	// event) by Session.Run, so it completes before the embedder is told
	// the turn is done.
	buildTurnConfig := func() chat.TurnConfig {
		return chat.TurnConfig{
			LLM:             cfg.LLM,
			Personality:     cfg.Personality,
			StatusLine:      cfg.StatusLine,
			WorkDir:         cfg.WorkDir,
			Store:           cfg.Store,
			Handlers:        handlers,
			Log:             chat.LogOrNoop(cfg.Log),
			Headless:        cfg.Headless,
			CustomTool:      cfg.CustomTool,
			CustomToolNames: cfg.CustomToolNames,
			ToolGuard:       cfg.ToolGuard,
			ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
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
			},
		}
	}

	launchTurn := func(userMsg llm.Message) {
		turnCtx, cancel := context.WithCancel(turnsCtx)
		app.SetBusy(true, cancel)
		tc := buildTurnConfig()
		// turnWG lets teardown join this goroutine before EndSession so its
		// store writes (saveHistory/compact) are never in flight when the
		// session is ended. Add(1) before spawning; Done() inside the goroutine.
		turnWG.Add(1)
		// Plain user-text messages go through Session.Run (which handles the
		// user_message event + history save). Messages with ContentParts (e.g.
		// /image) bypass Run since Run is string-only — saveHistory only
		// persists string Content anyway, so the image path drives RunTurn
		// directly.
		go func() {
			defer turnWG.Done()
			defer cancel()
			if len(userMsg.ContentParts) == 0 {
				sess.Run(turnCtx, tc, userMsg.Content)
				return
			}
			chat.RunTurn(turnCtx, tc, sess, userMsg, nil)
		}()
	}

	applyAgent := func(rt chat.ActiveAgent) {
		cfg.LLM = rt.LLM
		cfg.Personality = rt.Personality
		cfg.Params = rt.Params
		cfg.ToolGuard = rt.ToolGuard
		cfg.ModeLabel = rt.ModeLabel
		cfg.ActiveSkills = rt.ActiveSkills
		cfg.ActiveTools = rt.ActiveTools
		cfg.CustomToolNames = rt.CustomToolNames
		cfg.ContextWindow = rt.ContextWindow
		cfg.StatusLine = fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID)
		app.SetMode(rt.ModeLabel)
		app.SetStatus(cfg.StatusLine)
		app.SetContextWindow(rt.ContextWindow)
	}

	app.SetTab(func() {
		if cfg.SwitchAgent == nil || len(cfg.AgentNames) < 2 {
			return
		}
		cur := 0
		for i, n := range cfg.AgentNames {
			if n == cfg.ModeLabel {
				cur = i
				break
			}
		}
		next := cfg.AgentNames[(cur+1)%len(cfg.AgentNames)]
		rt, err := cfg.SwitchAgent(next)
		if err != nil {
			return
		}
		applyAgent(rt)
		app.PrintLine(patchtui.Dim + "[agent: " + rt.ModeLabel + "]" + patchtui.Reset)
	})

	registerSlashCommands(app, &cfg, sess, &lastUsage, launchTurn, applyAgent)

	app.SetSubmit(func(input string) {
		launchTurn(llm.Message{Role: llm.RoleUser, Content: input})
	})

	runErr = app.Run(ctx)
	return
}

// drainTurn consumes chat.Events for the lifetime of the session, updating
// App state. LLM text streams to scrollback line-by-line via patchmd (or
// verbatim inside fenced code blocks). Reasoning chunks stream to scrollback
// dim, also line-by-line. Tool calls render a header line; tool results
// render a dimmed body (or colorized diff for edit_file). TurnDone flushes
// the trailing partial line and clears busy.
//
// CONCURRENCY INVARIANT (busy-gate): drainTurn runs on its own long-lived
// goroutine and READS shared *chat.Config fields per event — e.g.
// cfg.CustomToolNames in renderToolCallHeader — and
// WRITES *lastUsage. The slash-command handlers in registerSlashCommands run on
// the input-loop goroutine and MUTATE the same struct (/agent,
// /clear, ... — plus Tab agent switching) and READ *lastUsage (/usage). There is deliberately NO mutex
// around cfg or lastUsage. This is race-free ONLY because of the busy-gate in
// patchapp: App.handleEnter (internal/patchapp/editor.go) early-returns while
// a.busy is true, so slash handlers (and SubmitFunc) cannot fire while a turn —
// and therefore drainTurn's per-event reads of cfg/lastUsage — is active.
// SetBusy(true) is set in launchTurn for the duration of the turn and cleared
// only when drainTurn processes the terminal TurnDone/Error event. A future
// maintainer who breaks that gate (e.g. allowing slash commands to run during
// streaming, or clearing busy before drainTurn finishes a turn) reintroduces a
// data race on cfg/lastUsage and must add real synchronization here.
func drainTurn(ch <-chan chat.Event, app patchapp.AppView, lastUsage *llm.Usage, cfg *chat.Config, sink *chat.OutSink) {
	var streamBuf strings.Builder
	// reasoningBuf holds an incomplete (no trailing \n) reasoning line.
	// Complete lines are committed to scrollback immediately for real-time display.
	var reasoningBuf strings.Builder
	reasoningStarted := false

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
	publishUsage := func(u llm.Usage) {
		if u == *lastUsage {
			return
		}
		*lastUsage = u
		if u.TotalTokens > 0 {
			app.SetTokens(u.TotalTokens)
		}
	}
	// publishEventUsage forwards a wire usage payload (if present) to
	// publishUsage. Shared by the usage and turn-done events.
	publishEventUsage := func(u *chat.EventUsageData) {
		if u == nil {
			return
		}
		publishUsage(llm.Usage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
		})
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

	for ev := range ch {
		if sink != nil {
			sink.WriteChatEvent(ev)
		}
		switch ev.Kind {
		case chat.EventAssistantReasoning:
			appendReasoning(ev.Text)

		case chat.EventAssistantToken:
			flushReasoningPartial()
			streamBuf.WriteString(ev.Text)
			flushStream()

		case chat.EventToolCall:
			// Render the per-tool header. Body arrives later via EventToolResult.
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(renderToolCallHeader(ev, cfg) + "\n"))

		case chat.EventToolResult:
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(renderToolResultBody(ev) + "\n\n"))

		case chat.EventSystemReminder:
			flushReasoningPartial()
			flushStreamFully()
			app.Print(patchtui.SplitLines(patchtui.Dim + ev.Text + patchtui.Reset + "\n\n"))

		case chat.EventRetry:
			// A transient failure is being retried (pre-token, so buffers are
			// empty). Render a dim notice and leave busy state untouched — the
			// turn is still in progress.
			app.Print(patchtui.SplitLines(patchtui.Dim + "⟳ " + ev.Text + patchtui.Reset + "\n"))

		case chat.EventUsage:
			publishEventUsage(ev.Usage)

		case chat.EventTurnDone:
			flushReasoningPartial()
			flushStreamFully()
			publishEventUsage(ev.Usage)
			app.SetBusy(false, nil)

		case chat.EventError:
			flushReasoningPartial()
			if streamBuf.Len() > 0 {
				app.Print(patchtui.SplitLines(streamBuf.String()))
				streamBuf.Reset()
			}
			msg := ev.Text
			if strings.Contains(msg, "context canceled") {
				app.PrintLine(patchtui.Dim + "[cancelled]" + patchtui.Reset)
			} else {
				app.PrintLine(patchtui.Red + "[error: " + msg + "]" + patchtui.Reset)
			}
			app.SetBusy(false, nil)

		case chat.EventSessionStart, chat.EventSessionEnd, chat.EventUserMessage, chat.EventAssistantMessage:
			// User input is shown via the input widget; full assistant message
			// is already streamed via tokens. Session lifecycle events are sink-only.
		}
	}
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

// registerSlashCommands wires up the slash registry. Closures capture cfg,
// sess, and lastUsage so handlers can read and mutate session state.
//
// CONCURRENCY: these handlers mutate the shared *chat.Config (e.g. /agent
// writes cfg.LLM/Params/Personality/ToolGuard/StatusLine/ContextWindow; /clear
// writes cfg.Personality.SystemPrompt) and read *lastUsage (/usage) with
// NO mutex, even though drainTurn concurrently reads cfg/writes lastUsage from
// another goroutine. This is safe ONLY because patchapp's busy-gate
// (App.handleEnter in internal/patchapp/editor.go) refuses to dispatch any
// slash handler while a.busy is true, and a turn (with drainTurn actively
// reading cfg) holds busy from launchTurn until drainTurn sees TurnDone/Error.
// So a slash handler and drainTurn never touch cfg/lastUsage at the same time.
// Keep that invariant intact, or add synchronization. See drainTurn for the
// matching note on the read side.
func registerSlashCommands(app slashTarget, cfg *chat.Config, sess *chat.Session, lastUsage *llm.Usage, launchTurn func(llm.Message), applyAgent func(chat.ActiveAgent)) {
	dim := func(s string) { app.PrintLine(patchtui.Dim + s + patchtui.Reset) }

	app.RegisterSlash(patchapp.SlashCommand{
		Name: "clear", Help: "reset conversation context",
		Handler: func(string) {
			sess.SetMessages(nil)
			// A new conversation starts now; re-stamp the system prompt so its
			// timestamp reflects the new context rather than process-start time.
			if cfg.RefreshPrompt != nil {
				cfg.Personality.SystemPrompt = cfg.RefreshPrompt()
			}
			dim("[context cleared]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "rollback", Help: "remove last turn from context",
		Handler: func(string) {
			msgs := sess.Messages()
			pruned := chat.PruneLastTurn(msgs)
			if len(pruned) == len(msgs) {
				dim("[nothing to roll back]")
				return
			}
			sess.SetMessages(pruned)
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
			msgs := sess.Messages()
			out := chat.PruneByID(id, "pruned by user", msgs)
			sess.SetMessages(msgs)
			dim("[" + out + "]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "usage", Help: "show token usage from last turn",
		Handler: func(string) {
			if lastUsage.TotalTokens == 0 {
				dim("[no usage data yet]")
				return
			}
			app.Print([]string{
				fmt.Sprintf("prompt:     %d", lastUsage.PromptTokens),
				fmt.Sprintf("completion: %d", lastUsage.CompletionTokens),
				fmt.Sprintf("total:      %d", lastUsage.TotalTokens),
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

			lines := []string{
				"",
				patchtui.Yellow + patchtui.Bold + "System prompt" + patchtui.Reset,
				patchtui.Dim + strings.Repeat("─", min(40, max(0, w-2))) + patchtui.Reset,
			}
			prompt := strings.TrimSpace(cfg.Personality.SystemPrompt)
			if prompt == "" {
				lines = append(lines, patchtui.Dim+"(empty)"+patchtui.Reset)
			} else {
				lines = append(lines, patchmd.Render(prompt, w-2)...)
			}

			lines = append(lines, "", patchtui.Cyan+patchtui.Bold+"Active tools"+patchtui.Reset)
			if len(cfg.Personality.Tools) == 0 {
				lines = append(lines, "  "+patchtui.Dim+"(none)"+patchtui.Reset)
			} else {
				for _, t := range cfg.Personality.Tools {
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
			for _, m := range sess.Messages() {
				if m.Role == llm.RoleTool && m.ToolCallID == id {
					body := strings.TrimRight(stripToolIDPrefix(m.Content), "\n")
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
			describer, _ := cfg.LLM.(llm.ParamDescriber)
			setter, _ := cfg.LLM.(llm.ParamSetter)

			args = strings.TrimSpace(args)
			if args == "" {
				if describer == nil {
					dim("[current provider exposes no parameters]")
					return
				}
				lines := []string{patchtui.Bold + "parameters:" + patchtui.Reset}
				for _, s := range describer.ParamSpecs() {
					cur := currentParamValue(cfg.Params, s.Name)
					if cur == "" {
						cur = "—"
					}
					enum := ""
					if len(s.Enum) > 0 {
						enum = " [" + strings.Join(s.Enum, "|") + "]"
					}
					def := s.Default
					if def == "" {
						def = "provider"
					}
					lines = append(lines, fmt.Sprintf("  %-22s = %-8s%s  (default %s)", s.Name, cur, enum, def))
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
			if describer != nil {
				var spec *llm.ParamSpec
				for _, s := range describer.ParamSpecs() {
					if s.Name == name {
						s := s
						spec = &s
						break
					}
				}
				if spec == nil {
					dim(fmt.Sprintf("[unknown parameter %q for this provider]", name))
					return
				}
				if err := spec.Validate(value); err != nil {
					dim(fmt.Sprintf("[%v]", err))
					return
				}
			}
			if err := cfg.Params.SetByName(name, value); err != nil {
				dim(fmt.Sprintf("[%v]", err))
				return
			}
			if setter != nil {
				setter.SetParams(cfg.Params)
			}
			if name == "reasoning_effort" {
				prov, model := chat.SplitStatus(cfg.StatusLine)
				if prov != "" && model != "" {
					cfg.StatusLine = chat.FormatStatus(prov, model, cfg.Params.ReasoningEffort)
					app.SetStatus(cfg.StatusLine)
				}
			}
			dim(fmt.Sprintf("[%s = %s]", name, value))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "agent", Help: "/agent [name] — list agents or switch the active agent",
		Handler: func(args string) {
			if cfg.SwitchAgent == nil || len(cfg.AgentNames) == 0 {
				dim("[no agents configured]")
				return
			}
			name := strings.TrimSpace(args)
			if name == "" {
				lines := []string{patchtui.Bold + "agents:" + patchtui.Reset}
				for _, n := range cfg.AgentNames {
					marker := ""
					if n == cfg.ModeLabel {
						marker = patchtui.Dim + "  (active)" + patchtui.Reset
					}
					lines = append(lines, "  "+n+marker)
				}
				lines = append(lines, "", patchtui.Dim+"usage: /agent <name>"+patchtui.Reset)
				app.Print(lines)
				return
			}
			rt, err := cfg.SwitchAgent(name)
			if err != nil {
				dim(fmt.Sprintf("[%v]", err))
				return
			}
			applyAgent(rt)
			dim(fmt.Sprintf("[agent: %s]", rt.ModeLabel))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "info", Help: "show session details: agent, project, skills, tools",
		Handler: func(string) {
			lines := []string{""}
			add := func(label, value string) {
				if value != "" {
					lines = append(lines, patchtui.Bold+label+patchtui.Reset)
					lines = append(lines, "    "+value)
				}
			}
			add("agent", cfg.ModeLabel)
			add("project", cfg.ProjectRef)
			if len(cfg.ActiveSkills) > 0 {
				lines = append(lines, patchtui.Bold+"skills"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(cfg.ActiveSkills, ", "))
			}
			if len(cfg.Personality.Tools) > 0 {
				lines = append(lines, patchtui.Bold+"tools"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(toolNames(cfg.Personality.Tools), ", "))
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
			msg, err := chat.BuildImageMessage(args, cfg.WorkDir)
			if err != nil {
				dim(fmt.Sprintf("[image: %v]", err))
				return
			}
			launchTurn(msg)
		},
	})
}

func toolNames(tools []llm.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func currentParamValue(p llm.RequestParams, name string) string {
	switch name {
	case "reasoning_effort":
		return p.ReasoningEffort
	case "parallel_tool_calls":
		if p.ParallelToolCalls == nil {
			return ""
		}
		return fmt.Sprintf("%t", *p.ParallelToolCalls)
	case "temperature":
		if p.Temperature == nil {
			return ""
		}
		return fmt.Sprintf("%g", *p.Temperature)
	case "max_tokens":
		if p.MaxTokens == 0 {
			return ""
		}
		return fmt.Sprintf("%d", p.MaxTokens)
	}
	return ""
}
