package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/models"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchmd"
	"github.com/weatherjean/shell3/internal/patchtui"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

// ModelChoice pairs a provider name with one of its models.
type ModelChoice struct {
	Provider string
	Model    string
}

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
	ProjectRef    string   // project UUID from .ref
	ActiveSkills  []string // skill names active for this persona
	ActiveTools   []string // user tool names active for this persona
	Models        []ModelChoice
	ModelSwitcher func(provider, model string) (LLMClient, error)
	Reloader      func() (persona.Persona, map[string]usertools.Tool, error)
	Truncate      bool
	Docs          string
	UserTools     map[string]usertools.Tool
	Secrets       map[string]string
	Params        llm.RequestParams
	Log           applog.Logger
}

// NewHandlers constructs the built-in tool handler map from a Config.
// Handlers are injected into TurnConfig and looked up by tool name during dispatch.
func NewHandlers(cfg Config) map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		EditHandler{},
		PruneHandler{},
		DocsHandler{docs: cfg.Docs},
		StoreHandler{toolName: "memory_upsert"},
		StoreHandler{toolName: "memory_list"},
		StoreHandler{toolName: "memory_search"},
		StoreHandler{toolName: "history_get"},
		StoreHandler{toolName: "history_search"},
	}
	m := make(map[string]ToolHandler, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
	}
	return m
}

// RunInteractive runs the TUI chat loop. Blocks until the user quits.
func RunInteractive(ctx context.Context, cfg Config) error {
	sess := &session{}

	if cfg.Store != nil {
		sessionID, err := cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("chat: start session: %w", err)
		}
		sess.id = sessionID
		// End whichever session is current when the loop exits. compact_history
		// may roll sess.id to a new session mid-conversation, so read sess.id
		// at defer time rather than capturing the initial sessionID.
		lg := logOrNoop(cfg.Log)
		defer func() {
			if err := cfg.Store.EndSession(sess.id); err != nil {
				lg.Warn("end session failed", "session_id", sess.id, "error", err)
			}
		}()
	}

	app := patchapp.New(cfg.ModeLabel, cfg.StatusLine, patchapp.WelcomeInfo{
		Persona:      cfg.ModeLabel,
		ProjectRef:   cfg.ProjectRef,
		ActiveSkills: cfg.ActiveSkills,
		ActiveTools:  cfg.ActiveTools,
	})
	if _, initModel := splitStatus(cfg.StatusLine); initModel != "" {
		app.SetContextWindow(models.ContextWindow(initModel))
	}
	cfg.Hooks.SetReleaser(app)
	cfg.Hooks.OnSessionStart(ctx)
	defer func() {
		cfg.Hooks.OnSessionEnd(ctx)
		cfg.Hooks.Wait() // drain background fire-and-forget hooks before teardown
	}()

	var lastUsage llm.Usage

	handlers := NewHandlers(cfg)

	// launchTurn starts a turn goroutine for userMsg and wires drain.
	launchTurn := func(userMsg llm.Message) {
		ch := make(chan patchapp.Event, 256)
		turnCtx, cancel := context.WithCancel(ctx)
		app.SetBusy(true, cancel)
		prevLen := len(sess.messages)
		tc := TurnConfig{
			LLM:         cfg.LLM,
			Hooks:       cfg.Hooks,
			Personality: cfg.Personality,
			StatusLine:  cfg.StatusLine,
			WorkDir:     cfg.WorkDir,
			Store:       cfg.Store,
			UserTools:   cfg.UserTools,
			Secrets:     cfg.Secrets,
			Truncate:    cfg.Truncate,
			Handlers:    handlers,
			Log:         logOrNoop(cfg.Log),
		}
		go func() {
			defer cancel()
			runTurn(turnCtx, tc, sess, userMsg, ch)
			saveHistory(cfg.Store, sess, sess.id, prevLen)
		}()
		go drainTurn(ch, app, &lastUsage, &cfg)
	}

	registerSlashCommands(app, &cfg, sess, &lastUsage, launchTurn)

	app.SetSubmit(func(input string) {
		launchTurn(llm.Message{Role: llm.RoleUser, Content: input})
	})

	return app.Run(ctx)
}

// drainTurn consumes events from a turn goroutine, updating App state.
// Streaming text accumulates into a buffer; on TurnDone the buffer is
// committed to scrollback and the App returns to idle.
func drainTurn(ch <-chan patchapp.Event, app patchapp.AppView, lastUsage *llm.Usage, cfg *Config) {
	var streamBuf strings.Builder
	// reasoningBuf holds an incomplete (no trailing \n) reasoning line.
	// Complete lines are committed to scrollback immediately for real-time display.
	var reasoningBuf strings.Builder
	reasoningStarted := false

	commitReasoningLine := func(line string) {
		app.Print([]string{patchtui.Dim + line + patchtui.Reset})
	}

	// flushReasoningPartial commits any buffered partial reasoning line, adds a
	// trailing blank line if thinking was shown, and clears the preview.
	flushReasoningPartial := func() {
		if reasoningBuf.Len() > 0 {
			commitReasoningLine(reasoningBuf.String())
			reasoningBuf.Reset()
		}
		if reasoningStarted {
			app.Print([]string{""})
			reasoningStarted = false
		}
		app.SetStreamPreview(nil)
	}

	flushPreview := func() {
		text := streamBuf.String()
		if text == "" {
			app.SetStreamPreview(nil)
			return
		}
		w, _ := patchtui.Size()
		app.SetStreamPreview(patchmd.Render(text, w-2))
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

	for ev := range ch {
		switch v := ev.(type) {
		case patchapp.ReasoningChunkEvent:
			// Commit each complete line to scrollback immediately (gray, real-time).
			// Show the current partial line in the stream preview so mid-line
			// progress is visible without the multi-line ANSI state bug.
			if !reasoningStarted {
				app.Print([]string{patchtui.Dim + "◆ thinking" + patchtui.Reset})
				reasoningStarted = true
			}
			text := v.Text
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
			if reasoningBuf.Len() > 0 {
				app.SetStreamPreview([]string{patchtui.Dim + reasoningBuf.String() + patchtui.Reset})
			} else {
				app.SetStreamPreview(nil)
			}

		case patchapp.ChunkEvent:
			flushReasoningPartial()
			streamBuf.WriteString(v.Text)
			flushPreview()

		case patchapp.AppendEvent:
			// Tool output. Commit any pending stream text first so order is preserved.
			flushReasoningPartial()
			if streamBuf.Len() > 0 {
				app.SetStreamPreview(nil)
				w, _ := patchtui.Size()
				app.Print(patchmd.Render(streamBuf.String(), w-2))
				streamBuf.Reset()
			}
			app.Print(patchtui.SplitLines(v.Text))

		case patchapp.UsageEvent:
			publishUsage(v.Usage)

		case patchapp.TurnDoneEvent:
			flushReasoningPartial()
			if streamBuf.Len() > 0 {
				w, _ := patchtui.Size()
				app.Print(patchmd.Render(streamBuf.String(), w-2))
				streamBuf.Reset()
			}
			publishUsage(v.Usage)
			app.SetBusy(false, nil)

		case patchapp.TurnErrEvent:
			flushReasoningPartial()
			if streamBuf.Len() > 0 {
				app.Print(patchtui.SplitLines(streamBuf.String()))
				streamBuf.Reset()
			}
			msg := v.Err.Error()
			if strings.Contains(msg, "context canceled") {
				app.PrintLine(patchtui.Dim + "[cancelled]" + patchtui.Reset)
			} else {
				app.PrintLine(patchtui.Red + "[error: " + msg + "]" + patchtui.Reset)
			}
			app.SetBusy(false, nil)

		case patchapp.TTYExecEvent:
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
func registerSlashCommands(app slashTarget, cfg *Config, sess *session, lastUsage *llm.Usage, launchTurn func(llm.Message)) {
	dim := func(s string) { app.PrintLine(patchtui.Dim + s + patchtui.Reset) }

	doReload := func() bool {
		if cfg.Reloader == nil {
			return true
		}
		newPers, newToolMap, err := cfg.Reloader()
		if err != nil {
			dim(fmt.Sprintf("[reload failed: %v]", err))
			return false
		}
		cfg.Personality = newPers
		cfg.UserTools = newToolMap
		return true
	}

	app.RegisterSlash(patchapp.SlashCommand{
		Name: "reload", Help: "rebuild system prompt from disk (memories, skills, tools)",
		Handler: func(string) {
			if doReload() {
				dim("[reloaded: memories, skills, and tools refreshed]")
			}
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "clear", Help: "reset conversation context and reload system prompt",
		Handler: func(string) {
			doReload()
			sess.messages = nil
			dim("[context cleared]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "rollback", Help: "remove last turn from context",
		Handler: func(string) {
			pruned := pruneLastTurn(sess.messages)
			if len(pruned) == len(sess.messages) {
				dim("[nothing to roll back]")
				return
			}
			sess.messages = pruned
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
			out := pruneByID(id, "pruned by user", sess.messages)
			dim("[" + out + "]")
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "model", Help: "switch model: /model [provider/model] (no arg → picker)",
		Handler: func(args string) {
			curProv, curModel := splitStatus(cfg.StatusLine)
			arg := strings.TrimSpace(args)
			var choice ModelChoice
			if arg == "" {
				if len(cfg.Models) < 2 {
					dim("[/model usage: /model <provider/model>]")
					return
				}
				picked, ok := pickModel(app, cfg.Models, curProv, curModel)
				if !ok {
					return
				}
				choice = picked
			} else {
				resolved, ok := resolveModelArg(cfg.Models, arg, curProv)
				if !ok {
					dim(fmt.Sprintf("[unknown model: %s]", arg))
					return
				}
				choice = resolved
			}
			if cfg.ModelSwitcher == nil {
				dim("[no model switcher configured]")
				return
			}
			newClient, err := cfg.ModelSwitcher(choice.Provider, choice.Model)
			if err != nil {
				dim(fmt.Sprintf("[model switch failed: %v]", err))
				return
			}
			if newClient != nil {
				cfg.LLM = newClient
				if setter, ok := newClient.(llm.ParamSetter); ok {
					setter.SetParams(cfg.Params)
				}
			}
			cfg.StatusLine = formatStatus(choice.Provider, choice.Model, cfg.Params.ReasoningEffort)
			app.SetStatus(cfg.StatusLine)
			app.SetContextWindow(models.ContextWindow(choice.Model))
			dim(fmt.Sprintf("[model: %s/%s]", choice.Provider, choice.Model))
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
		Name: "truncate", Help: "toggle truncated bash output",
		Handler: func(string) {
			cfg.Truncate = !cfg.Truncate
			state := "off"
			if cfg.Truncate {
				state = "on"
			}
			dim(fmt.Sprintf("[full output: %s]", state))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "parameters",
		Help: "/parameters [name value] — list or set tunable params (reasoning_effort, verbosity, ...)",
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
					enum := ""
					if len(s.Enum) > 0 {
						enum = " [" + strings.Join(s.Enum, "|") + "]"
					}
					lines = append(lines, fmt.Sprintf("  %-22s %s%s  (default %s)", s.Name, cur, enum, s.Default))
				}
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
				prov, model := splitStatus(cfg.StatusLine)
				if prov != "" && model != "" {
					cfg.StatusLine = formatStatus(prov, model, cfg.Params.ReasoningEffort)
					app.SetStatus(cfg.StatusLine)
				}
			}
			dim(fmt.Sprintf("[%s = %s]", name, value))
		},
	})
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "info", Help: "show session details: persona, project, skills, tools, hooks",
		Handler: func(string) {
			lines := []string{""}
			add := func(label, value string) {
				if value != "" {
					lines = append(lines, patchtui.Bold+label+patchtui.Reset)
					lines = append(lines, "    "+value)
				}
			}
			add("persona", cfg.ModeLabel)
			add("project", cfg.ProjectRef)
			if len(cfg.ActiveSkills) > 0 {
				lines = append(lines, patchtui.Bold+"skills"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(cfg.ActiveSkills, ", "))
			}
			if len(cfg.Personality.Tools) > 0 {
				lines = append(lines, patchtui.Bold+"tools"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(toolNames(cfg.Personality.Tools), ", "))
			}
			hcfg := cfg.Personality.Config.Config
			var activeHooks []string
			for name, entry := range map[string]string{
				"on_session_start": hcfg.OnSessionStart.Command,
				"on_session_end":   hcfg.OnSessionEnd.Command,
				"on_turn_start":    hcfg.OnTurnStart.Command,
				"on_turn_end":      hcfg.OnTurnEnd.Command,
				"on_tool_call":     hcfg.OnToolCall.Command,
				"on_tool_result":   hcfg.OnToolResult.Command,
				"on_context_build": hcfg.OnContextBuild.Command,
				"on_error":         hcfg.OnError.Command,
			} {
				if entry != "" {
					activeHooks = append(activeHooks, name)
				}
			}
			if len(activeHooks) > 0 {
				sort.Strings(activeHooks)
				lines = append(lines, patchtui.Bold+"hooks"+patchtui.Reset)
				lines = append(lines, "    "+strings.Join(activeHooks, ", "))
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
			msg, err := buildImageMessage(args, cfg.WorkDir)
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
	case "reasoning_summary":
		return p.ReasoningSummary
	case "verbosity":
		return p.Verbosity
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
	}
	return ""
}

// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg Config, input string) error {
	sess := &session{}
	ch := make(chan patchapp.Event, 256)
	tc := TurnConfig{
		LLM:         cfg.LLM,
		Hooks:       cfg.Hooks,
		Personality: cfg.Personality,
		StatusLine:  cfg.StatusLine,
		WorkDir:     cfg.WorkDir,
		Store:       cfg.Store,
		UserTools:   cfg.UserTools,
		Secrets:     cfg.Secrets,
		Truncate:    cfg.Truncate,
		Handlers:    NewHandlers(cfg),
		Log:         logOrNoop(cfg.Log),
	}
	go runTurn(ctx, tc, sess, llm.Message{Role: llm.RoleUser, Content: input}, ch)

	for ev := range ch {
		switch v := ev.(type) {
		case patchapp.ChunkEvent:
			fmt.Print(v.Text)
		case patchapp.AppendEvent:
			fmt.Print(v.Text)
		case patchapp.TurnErrEvent:
			fmt.Fprintln(os.Stderr, "error:", v.Err)
		case patchapp.TurnDoneEvent:
			fmt.Println()
		}
	}
	return nil
}

// pruneLastTurn removes the last user message and everything after it.
// logOrNoop returns l if non-nil, otherwise a Noop logger.
// Callers that did not configure a logger get silent behaviour rather than a nil panic.
func logOrNoop(l applog.Logger) applog.Logger {
	if l != nil {
		return l
	}
	return applog.Noop{}
}

func pruneLastTurn(messages []llm.Message) []llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			return messages[:i]
		}
	}
	return messages
}
