// Package shell3 embeds the shell3 coding agent as a library. It exposes a
// persistent multi-turn Session (the plugin equivalent of an open TUI) plus a
// one-shot Run convenience, both streaming structured Events. The Session loads
// the same shell3.lua config, store, and persona as the CLI by building
// on internal/agentsetup. internal/chat, internal/persona, and internal/llm
// are implementation details, not part of this package's public API.
package shell3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// Spec configures Run / Start. Prompt is used by Run only.
type Spec struct {
	Prompt     string
	ConfigPath string // "" → ./shell3.lua then ~/.shell3/shell3.lua
	WorkDir    string // "" → os.Getwd()
	Agent      string // "" → first declared agent; unknown name fails Start/Run
	// Interactive flips the underlying build out of headless mode. The zero
	// value (false) preserves the historical headless behavior: the
	// shell_interactive tool is stripped from the schema and a system-reminder
	// explains the constraint. Set true for a TUI-style front-end that can
	// release the terminal for an interactive shell (see ShellInteractive).
	Interactive bool
	// OutPath, when non-empty, opens a JSONL audit log at this path. The
	// Session owns the sink: it writes a "start" line on Start, every internal
	// chat.Event (lossless, before public translation) during each turn, and an
	// "end" line on Close. Independent of the public Event stream.
	OutPath string
	// ShellInteractive runs an interactive shell command with TTY access and
	// returns the result string recorded as tool output. nil keeps the
	// shell_interactive tool returning an "unavailable" string. A TUI supplies
	// a closure that releases the terminal for the duration of the command.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
}

// Kind discriminates a streamed Event.
type Kind int

const (
	Token          Kind = iota // assistant text         → Text
	Reasoning                  // thinking text           → Text
	ToolCall                   // tool started            → ToolName, ToolCallID, ToolInput, IsCustomTool
	ToolResult                 // tool finished           → ToolName, ToolCallID, ToolOutput
	SystemReminder             // injected reminder block → Text
	Usage                      // per-roundtrip tokens    → PromptTokens/CompletionTokens/TotalTokens
	Retry                      // transient retry         → Text
	Error                      // turn error              → Err
	Done                       // turn end (normal)       → token fields (final totals)
)

// Event is one item streamed on a Send/Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind             Kind
	Text             string // Token, Reasoning, Retry, SystemReminder
	ToolName         string // ToolCall, ToolResult
	ToolCallID       string // ToolCall, ToolResult (links a call to its result)
	ToolInput        string // ToolCall (raw JSON args)
	ToolOutput       string // ToolResult
	IsCustomTool     bool   // ToolCall (resolved against the active agent's custom-tool set)
	PromptTokens     int    // Usage, Done
	CompletionTokens int    // Usage, Done
	TotalTokens      int    // Usage, Done
	Err              error  // Error
}

// translate maps an internal chat.Event to a public Event. ok is false when the
// internal event has no public equivalent (session lifecycle, echoed user
// message, post-stream assistant message).
//
// translate is pure: it does NOT resolve Event.IsCustomTool, which depends on
// the session's current agent config. route sets that field after translate
// (see route), so this stays a config-free, table-testable mapping.
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventAssistantReasoning:
		return Event{Kind: Reasoning, Text: ev.Text}, true
	case chat.EventToolCall:
		return Event{Kind: ToolCall, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, ToolInput: ev.ToolInput}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, ToolOutput: ev.ToolOutput}, true
	case chat.EventSystemReminder:
		return Event{Kind: SystemReminder, Text: ev.Text}, true
	case chat.EventUsage:
		return usageEvent(Usage, ev), true
	case chat.EventTurnDone:
		return usageEvent(Done, ev), true
	case chat.EventRetry:
		return Event{Kind: Retry, Text: ev.Text}, true
	case chat.EventError:
		err := ev.Err
		if err == nil { // defensive: older/internal emitters may set only Text
			err = errors.New(ev.Text)
		}
		return Event{Kind: Error, Err: err}, true
	default:
		return Event{}, false
	}
}

func usageEvent(k Kind, ev chat.Event) Event {
	e := Event{Kind: k}
	if ev.Usage != nil {
		e.PromptTokens = ev.Usage.PromptTokens
		e.CompletionTokens = ev.Usage.CompletionTokens
		e.TotalTokens = ev.Usage.TotalTokens
	}
	return e
}

// Session is a live, multi-turn conversation — the plugin equivalent of an open
// TUI. It wraps one persistent chat.Session and the full agent config, and
// streams a per-Send channel of translated Events. Drain a Send channel to
// completion before the next Send/Clear/Rollback/SwitchAgent.
//
// The underlying chat.Session runs in synchronous-sink mode: each turn's events
// are delivered inline on the turn goroutine, which translates them onto the
// current Send channel and closes it when the turn returns. There is no
// long-lived drain goroutine and no event channel to close — "turn finished" is
// simply "the turn goroutine returned".
type Session struct {
	cfg      chat.Config
	sess     *chat.Session
	handlers map[string]chat.ToolHandler
	cleanup  func()

	// shellInteractive is Spec.ShellInteractive, threaded into every turn's
	// TurnConfig (see turnConfig). nil keeps shell_interactive "unavailable".
	shellInteractive func(ctx context.Context, cmd, workdir string) string

	// sink is the JSONL audit log, opened by Start when Spec.OutPath != "".
	// route writes every internal chat.Event to it (lossless) before
	// translating to a public Event; Close writes the "end" line. nil when no
	// OutPath was configured. sinkCleanup closes the underlying file.
	sink        *chat.OutSink
	sinkCleanup func()

	// mu guards the current turn's routing target and lifecycle handles.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns
	curDone    <-chan struct{}    // current turn ctx's Done; unblocks a send to an abandoned cur on Close
	turnCancel context.CancelFunc // cancels the in-flight turn (nil before the first Send)
	turnDone   chan struct{}      // closed when the turn goroutine returns (nil before the first Send)
	sawError   bool               // any turn emitted an error event; drives the audit "end" status
}

// Start loads the config (identically to the TUI), starts the store session,
// and launches the event drain. A non-nil error means startup failed and no
// Session was created.
func Start(ctx context.Context, spec Spec) (*Session, error) {
	workDir := spec.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: spec.ConfigPath,
		CWD:        workDir,
		HomeDir:    homeDir,
		// Interactive's zero value (false) maps to Headless:true, preserving the
		// historical headless-only behavior for embedders that don't opt in.
		Headless: !spec.Interactive,
		OutPath:  spec.OutPath,
		Agent:    spec.Agent,
	})
	if err != nil {
		return nil, err
	}

	s := newSession(cfg, cleanup)
	s.shellInteractive = spec.ShellInteractive

	// Open the audit log (if requested) and write its opening "start" line.
	// route streams every internal event into it for the rest of the session;
	// Close writes the "end" line. Mirrors how internal/tui/once.go and
	// interactive.go own their sinks.
	sink, sinkCleanup, err := chat.OpenSink(spec.OutPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	s.sink = sink
	s.sinkCleanup = sinkCleanup
	if sink != nil {
		label := spec.Prompt
		if label == "" {
			label = "(interactive)"
		}
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart(label, cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}
	return s, nil
}

// newSession wires a Session around an already-built chat.Config. The
// chat.Session runs in synchronous-sink mode: route translates each internal
// event and forwards it to the current Send channel inline on the turn
// goroutine. Split out from Start so tests can inject a fakellm-backed config.
func newSession(cfg chat.Config, cleanup func()) *Session {
	var storeID int64
	if cfg.Store != nil {
		if id, err := cfg.Store.StartSession(); err == nil {
			storeID = id
		} else {
			// Best-effort: a failed StartSession leaves storeID 0 (no
			// persistence). Log it at Warn so the silent non-persistence is
			// observable rather than vanishing.
			chat.LogOrNoop(cfg.Log).Warn("start session failed", "error", err)
		}
	}
	s := &Session{
		cfg:      cfg,
		handlers: chat.NewHandlers(cfg),
		cleanup:  cleanup,
		// Default to a no-op so Close is safe even when Start didn't open a
		// sink (and for tests that build a Session via newSession directly).
		sinkCleanup: func() {},
	}
	s.sess = chat.NewSession(chat.SessionOpts{
		StoreID:          storeID,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
		Sink:             s.route,
	})
	return s
}

// route is the chat.Session event sink. It runs synchronously on the in-flight
// turn goroutine, so all forwarding to a given Send channel happens-before that
// turn goroutine closes it — no separate drain, no close-ordering hazard. The
// select on curDone lets Close cancel the turn unblock a send to a Send channel
// the caller stopped reading. Events with no public equivalent are dropped.
//
// NOTE: curDone is the turn ctx's Done, which is also closed by an ordinary
// turn cancel (Ctrl-C/ESC), not just Close. So once a turn is cancelled this
// select MAY take the curDone branch and drop whatever it was delivering —
// INCLUDING the turn's terminal Done/Error event. Consumers must therefore
// treat channel close (see Send) as the authoritative end-of-turn
// signal; the terminal event is best-effort and can be absent on a cancel.
func (s *Session) route(ev chat.Event) {
	// Audit first, losslessly: the internal chat.Event keeps ToolCallID, system
	// reminders, and full untruncated content even though the public Event below
	// is a lossy projection. Independent of whether the event has a public form.
	if s.sink != nil {
		s.sink.WriteChatEvent(ev)
	}
	if ev.Kind == chat.EventError {
		s.mu.Lock()
		s.sawError = true
		s.mu.Unlock()
	}
	pub, ok := translate(ev)
	if !ok {
		return
	}
	// IsCustomTool can't be resolved in the pure translate (it has no config);
	// resolve it here against the session's current agent custom-tool set.
	if pub.Kind == ToolCall && s.cfg.CustomToolNames[pub.ToolName] {
		pub.IsCustomTool = true
	}
	s.mu.Lock()
	cur, done := s.cur, s.curDone
	s.mu.Unlock()
	if cur == nil {
		return
	}
	select {
	case cur <- pub:
	case <-done:
	}
}

// Send runs one turn for prompt and returns a channel of that turn's events,
// closed when the turn ends (the deferred close(out) below always runs).
// Channel close is the authoritative end-of-turn signal: a terminal Done/Error
// event is emitted before close on a best-effort basis but may be dropped on
// cancel (see route), so consumers must bind end-of-turn UI/state transitions
// to close, not to receiving Done/Error.
//
// Single-turn-at-a-time contract: the caller MUST drain the returned channel
// to completion before calling Send, Clear, Rollback, or SwitchAgent again.
// Those methods read and mutate unsynchronized session state (messages, cfg)
// and assume exactly one turn is active; overlapping them is a data race, not
// a supported concurrency mode.
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	out := make(chan Event)
	turnCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	s.cur = out
	s.curDone = turnCtx.Done()
	s.turnCancel = cancel
	s.turnDone = done
	s.mu.Unlock()
	tc := s.turnConfig()
	go func() {
		// route forwards events to out during the turn; once the turn returns no
		// further forwarding can happen, so clearing cur and closing out here
		// is race-free (both run on this goroutine, strictly after the turn).
		defer func() {
			s.mu.Lock()
			if s.cur == out {
				s.cur = nil
			}
			s.mu.Unlock()
			close(out)
			cancel() // release the child ctx
		}()
		defer close(done)
		s.sess.Run(turnCtx, tc, prompt)
	}()
	return out
}

// ID returns the store session id (rolls on compaction; "0" with no store).
func (s *Session) ID() string {
	return fmt.Sprintf("%d", s.sess.ID())
}

// Close ends the conversation: cancels any in-flight turn, waits for it to
// finish (so its deferred history persist runs against the still-open store),
// then ends the store session and releases the config.
//
// Close is robust to an abandoned Send channel: cancelling the turn ctx unblocks
// route's send to an unread channel (its curDone select fires), so the turn
// unwinds and the join below can't wedge. Draining the channel is still the
// supported pattern, but Close does not require it.
//
// Returns the store's EndSession error if ending the persisted session fails;
// the other best-effort teardown steps (turn cancel, cleanup) do not contribute
// to the returned error.
func (s *Session) Close() error {
	// Cancel any in-flight turn so it stops streaming and runs its deferred
	// history persist, then join it before ending the store session so a
	// cancelled turn isn't still writing to the store as EndSession runs.
	s.mu.Lock()
	cancel := s.turnCancel
	done := s.turnDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done // turn goroutine (and its deferred history persist) has finished
	}

	s.sess.End("ok")
	var endErr error
	if s.cfg.Store != nil {
		endErr = s.cfg.Store.EndSession(s.sess.ID())
	}
	// Flush the audit log: by here the turn goroutine has joined, so no route
	// call can still be writing to the sink. Then release the file and config.
	if s.sink != nil {
		status := "ok"
		s.mu.Lock()
		if s.sawError {
			status = "error"
		}
		s.mu.Unlock()
		s.sink.WriteEnd(status)
	}
	s.sinkCleanup()
	s.cleanup()
	return endErr
}

// turnConfig derives the per-turn config from the current cfg. Built fresh each
// turn so SwitchAgent's mutations to cfg take effect on the next Send.
//
// The interactive-shell runner is Spec.ShellInteractive (stored at Start). When
// nil — the default for a headless embedder — shell_interactive tool calls
// return an "unavailable" string instead of releasing a TTY.
func (s *Session) turnConfig() chat.TurnConfig {
	shellInteractive := s.shellInteractive
	if shellInteractive == nil {
		shellInteractive = func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in plugin mode"
		}
	}
	return chat.NewTurnConfig(s.cfg, s.handlers, shellInteractive)
}

// Clear resets the conversation context (= /clear): drops all history and
// re-stamps the system prompt with a fresh timestamp. Call only between turns:
// drain any in-flight Send channel first (see Send's contract).
func (s *Session) Clear() {
	s.sess.SetMessages(nil)
	if s.cfg.RefreshPrompt != nil {
		s.cfg.Personality.SystemPrompt = s.cfg.RefreshPrompt()
	}
}

// Rollback drops the last turn from context (= /rollback). Returns false when
// there was nothing to remove. Call only between turns: drain any in-flight
// Send channel first (see Send's contract).
func (s *Session) Rollback() bool {
	msgs := s.sess.Messages()
	pruned := chat.PruneLastTurn(msgs)
	if len(pruned) == len(msgs) {
		return false
	}
	s.sess.SetMessages(pruned)
	return true
}

// SwitchAgent activates the configured agent named name for subsequent Sends
// (= the TUI's /agent <name> or Tab). Switching swaps the agent's model client,
// system prompt, tool set, guard chain, custom-tool routing, skills, status
// line, and context window while keeping conversation history. Returns an error
// for an unknown agent or when the config declares no agents. Call only between
// turns: it mutates cfg in place, which the next Send's turnConfig reads, so
// drain any in-flight Send channel first (see Send's contract).
func (s *Session) SwitchAgent(name string) error {
	if s.cfg.SwitchAgent == nil {
		return fmt.Errorf("no agents configured")
	}
	rt, err := s.cfg.SwitchAgent(name)
	if err != nil {
		return err
	}
	s.cfg.ApplyActiveAgent(rt)
	return nil
}

// AgentNames returns the configured agent names in declaration order — the set
// SwitchAgent accepts. A caller can cycle (Tab-style) by finding ActiveAgent in
// this list and switching to the next entry. Empty or single-element means no
// switching is available.
func (s *Session) AgentNames() []string { return s.cfg.AgentNames }

// ActiveAgent returns the name of the currently active agent.
func (s *Session) ActiveAgent() string { return s.cfg.ModeLabel }

// ToolInfo names a tool exposed by the active agent and its one-line
// description, for introspection (the TUI's /prompt and /info).
type ToolInfo struct {
	Name        string
	Description string
}

// ParamValue is one tunable provider parameter and its current/default state,
// for introspection (the TUI's /parameters list). Enum is empty for freeform
// params. Value is "" when the param is at its provider default (unset).
type ParamValue struct {
	Name        string
	Value       string
	Default     string
	Description string
	Enum        []string
}

// Snapshot is a read-only view of the session's current agent state: everything
// the TUI's welcome banner, status bar, /prompt, /info, and /parameters list
// need, in one allocation. It is a point-in-time copy; mutate the Session (e.g.
// SwitchAgent, SetParam) and call Snapshot again to observe changes. Call only
// between turns (same contract as Clear/Rollback): it reads unsynchronized cfg.
type Snapshot struct {
	Agent         string
	Model         string
	ProjectRef    string
	StatusLine    string
	ContextWindow int
	SystemPrompt  string
	Tools         []ToolInfo
	Skills        []string
	Params        []ParamValue
}

// Snapshot returns the current agent state (see Snapshot). Params is populated
// only when the active provider implements llm.ParamDescriber; each entry's
// Value mirrors the TUI's currentParamValue mapping.
func (s *Session) Snapshot() Snapshot {
	_, model := chat.SplitStatus(s.cfg.StatusLine)
	snap := Snapshot{
		Agent:         s.cfg.ModeLabel,
		Model:         model,
		ProjectRef:    s.cfg.ProjectRef,
		StatusLine:    s.cfg.StatusLine,
		ContextWindow: s.cfg.ContextWindow,
		SystemPrompt:  s.cfg.Personality.SystemPrompt,
		Skills:        s.cfg.ActiveSkills,
	}
	for _, t := range s.cfg.Personality.Tools {
		snap.Tools = append(snap.Tools, ToolInfo{Name: t.Name, Description: t.Description})
	}
	if describer, ok := s.cfg.LLM.(llm.ParamDescriber); ok {
		for _, spec := range describer.ParamSpecs() {
			snap.Params = append(snap.Params, ParamValue{
				Name:    spec.Name,
				Value:   currentParamValue(s.cfg.Params, spec.Name),
				Default: spec.Default,
				Enum:    spec.Enum,
			})
		}
	}
	return snap
}

// HistoryEntry is one stored conversation message, projected for introspection
// (the TUI's /print). Content is already stripped of the internal
// "[tool_call_id=…]\n" storage prefix that tool results carry. Role is the
// plain string "user"/"assistant"/"tool"/"system".
type HistoryEntry struct {
	Role       string
	Content    string
	ToolName   string
	ToolCallID string
}

// History returns the current conversation history as public HistoryEntry
// values. Tool-role messages have their internal "[tool_call_id=…]\n" prefix
// stripped from Content so embedders see the raw tool output. Call only between
// turns (it reads unsynchronized session state).
func (s *Session) History() []HistoryEntry {
	msgs := s.sess.Messages()
	out := make([]HistoryEntry, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if m.Role == llm.RoleTool {
			content = stripToolIDPrefix(content)
		}
		out = append(out, HistoryEntry{
			Role:       string(m.Role),
			Content:    content,
			ToolName:   m.Name,
			ToolCallID: m.ToolCallID,
		})
	}
	return out
}

// stripToolIDPrefix removes the "[tool_call_id=…]\n" prefix the turn loop
// prepends to each stored tool result's content, leaving just the raw output,
// so the public projection in History hides the internal storage detail.
func stripToolIDPrefix(content string) string {
	if strings.HasPrefix(content, "[tool_call_id=") {
		if nl := strings.IndexByte(content, '\n'); nl >= 0 {
			return content[nl+1:]
		}
	}
	return content
}

// Prune replaces the tool result with the given tool-call id by a short stub,
// freeing its context-window space (= the TUI's /prune <id>). summary is the
// human-readable status string; ok is false when no tool result with that id
// exists in the conversation. Call only between turns (mutates history).
func (s *Session) Prune(id string) (summary string, ok bool) {
	msgs := s.sess.Messages()
	out := chat.PruneByID(id, "pruned by user", msgs)
	s.sess.SetMessages(msgs)
	// PruneByID reports a missing id with an "error: no tool result with id …"
	// prefix; treat any other (success) string as ok.
	if strings.HasPrefix(out, "error: no tool result with id") {
		return out, false
	}
	return out, true
}

// SetParam sets a tunable provider parameter for subsequent turns (= the TUI's
// /parameters <name> <value>). When the active provider implements
// llm.ParamDescriber the value is validated against that param's spec first;
// the new params are then pushed to the provider if it implements
// llm.ParamSetter. Setting reasoning_effort also re-derives the status line so
// the next Snapshot reflects it. Call only between turns (mutates cfg).
func (s *Session) SetParam(name, value string) error {
	describer, _ := s.cfg.LLM.(llm.ParamDescriber)
	setter, _ := s.cfg.LLM.(llm.ParamSetter)

	if describer != nil {
		var spec *llm.ParamSpec
		for _, sp := range describer.ParamSpecs() {
			if sp.Name == name {
				sp := sp
				spec = &sp
				break
			}
		}
		if spec == nil {
			return fmt.Errorf("unknown parameter %q for this provider", name)
		}
		if err := spec.Validate(value); err != nil {
			return err
		}
	}
	if err := s.cfg.Params.SetByName(name, value); err != nil {
		return err
	}
	if setter != nil {
		setter.SetParams(s.cfg.Params)
	}
	if name == "reasoning_effort" {
		prov, model := chat.SplitStatus(s.cfg.StatusLine)
		if prov != "" && model != "" {
			s.cfg.StatusLine = chat.FormatStatus(prov, model, s.cfg.Params.ReasoningEffort)
		}
	}
	return nil
}

// currentParamValue maps a RequestParams field to its display string for the
// given /parameters name. The TUI renders Snapshot's ParamValue.Value directly,
// so this is the single source of that mapping. "" means "unset (provider
// default)".
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

// Run is the one-shot convenience: Start, send spec.Prompt, stream the turn,
// and Close when it drains. A non-nil error means startup failed.
//
// Close always runs once the caller drains the returned channel: the turn
// emits exactly one terminal event (Done, or Error on ctx cancellation), which
// closes the inner turn channel and ends the forwarding range below.
func Run(ctx context.Context, spec Spec) (<-chan Event, error) {
	s, err := Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	turn := s.Send(ctx, spec.Prompt)
	out := make(chan Event)
	go func() {
		defer close(out)
		defer s.Close()
		for ev := range turn {
			out <- ev
		}
	}()
	return out, nil
}
