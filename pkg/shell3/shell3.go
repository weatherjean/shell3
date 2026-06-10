// Package shell3 embeds the shell3 coding agent as a library — from a one-shot
// prompt to an always-on personal agent hosting many concurrent chats. It loads
// the same shell3.lua config, store, and persona as the CLI by building on
// internal/agentsetup; internal/chat, internal/persona, and internal/llm are
// implementation details, not part of this package's public API.
//
// # Three entry points
//
// Run executes a single prompt and streams the turn's [Event]s until the
// channel closes (the session is built and torn down for you). Start gives a
// persistent multi-turn [Session] — the embedding equivalent of an open TUI —
// with agent switching, history, pruning, and parameter control. NewRuntime is
// the host shape: one [Runtime] owning the shared build (config, store, MCP,
// proxy spawner, log) and hosting N named [Session]s via [Runtime.Session].
// Start and Run are thin single-session wrappers over a Runtime.
//
// # Sessions and the single-turn contract
//
// A Session runs one turn at a time. [Session.Send] streams a turn's events and
// returns [ErrBusy] (as an Error event) if a turn is already in flight; drain
// the channel to completion before the next Send, Clear, SwitchAgent, or Prune.
// Name sessions on the runtime (e.g. "tg:1234") via [SessionOpts]; requesting an
// existing live name returns that session. Each session has its own agent,
// workdir, headless flag, and audit log.
//
// # Steering: inbox and Interject
//
// [Session.Interject] queues a message (and optional media [Part]s) from any
// goroutine. It never fails: while a turn runs the text is injected at the next
// round boundary as a system-reminder that the user sent input; while idle it is queued and
// the session Wakes (see below). [Session.Send] is the strict path that honors
// ErrBusy; [Session.SendParts] is Send with media attachments.
// [Session.RunQueued] runs one turn seeded from the queued inbox items — the
// host's response to a Wake — and no-ops on an empty inbox or a busy session.
//
// # Out-of-turn bus: Wake
//
// A long-lived host does not block on a single Send channel. [Runtime.Events]
// returns a shared <-chan [HostEvent]; an inbox gaining an item while the
// session is idle emits a [HostEvent] of kind [Wake] naming the session. The
// host runs one select loop: receive a HostEvent, match HostEvent.Session
// against each Session's Name, and call RunQueued to react. A single-session
// host created via Start can use [Session.WakeEvents] instead of holding a
// *Runtime. The bus is buffered and drops on a full buffer (Wake is a hint, not
// a queue — the next turn drains the inbox anyway).
//
//	rt, _ := shell3.NewRuntime(shell3.RuntimeSpec{WorkDir: home})
//	defer rt.Close()
//	sessions := map[string]*shell3.Session{ /* name → session */ }
//	for ev := range rt.Events() {
//		if ev.Kind != shell3.Wake {
//			continue
//		}
//		if s := sessions[ev.Session]; s != nil {
//			for e := range s.RunQueued(context.Background()) {
//				_ = e // stream tokens/tool calls to the chat surface
//			}
//		}
//	}
//
// # Approval
//
// A Lua guard may return an "ask" verdict to suspend a tool call for human
// approval. The host supplies [SessionOpts].Approve (or [Session.SetApprover],
// or [Spec].Approve) — a func(context.Context, [ApprovalRequest]) bool that
// blocks the turn goroutine until the host answers. The [ApprovalRequest]
// carries the Tool, RawArgs, Reason, and Agent. A nil approver fails closed: the
// ask degrades to a deny. Requests and verdicts are recorded in the audit log.
//
// # Inbound media
//
// SendParts and Interject accept []Part / ...Part attachments. A [Part] sets
// exactly one of Path (extension-routed) or Data (MIME-routed, MIME required) —
// so Telegram photos and voice notes never touch disk — with Kind [PartImage]
// or [PartAudio]. Size caps match read_media (10 MB images, 25 MB audio).
// SendParts is all-or-nothing (one invalid part rejects the turn with an Error);
// Interject drops invalid parts with a bracketed note and still delivers.
//
// # Subagents
//
// Subagents are an explicit registry of delegatable specialists. Declare one
// with shell3.subagent{name, description, ...} (the description is the
// model-facing "when to use"); it is not part of the Tab/agent rotation. An
// agent opts in by listing subagent handles: tools = { subagents = { explorer,
// researcher } }. Such an agent gets one spawn_agent(task, subagent, workdir?)
// tool whose subagent parameter is an enum of the registered names (each name
// and its when-to-use listed in the tool description), plus list_agents.
//
// spawn_agent runs the chosen subagent's own config (model, prompt, tools) as a
// headless "sub:<id>" session on the shared Runtime; its result is posted to the
// parent's inbox (injected mid-turn if the parent is still working, or delivered
// as a Wake if idle). Subagents are depth-limited to 1 — they get no spawn tools
// and may not declare their own subagents — and write their own audit JSONL
// under .shell3/agents/.
package shell3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// ApprovalRequest describes a suspended tool call awaiting the host's verdict
// (a Lua guard returned action="ask"). Render it and return true to allow.
type ApprovalRequest struct {
	Tool    string // tool name
	RawArgs string // raw JSON arguments
	Reason  string // the guard's stated reason ("" if none)
	Agent   string // active agent name
}

// PartKind discriminates a Part's media type.
type PartKind int

const (
	PartImage PartKind = iota // jpg/png/gif/webp → resized JPEG data URI
	PartAudio                 // wav/mp3 → base64 input_audio
)

// String returns "image"/"audio" for error messages.
func (k PartKind) String() string {
	switch k {
	case PartImage:
		return "image"
	case PartAudio:
		return "audio"
	default:
		return fmt.Sprintf("PartKind(%d)", int(k))
	}
}

// Part is one inbound media attachment for SendParts and Interject. Set
// exactly one of Path or Data. With Data, MIME is required ("image/png",
// "audio/mpeg", …) and selects the handling; with Path, routing is by file
// extension and MIME is ignored. Relative paths resolve against the session
// workdir. Size caps match read_media: 10 MB images, 25 MB audio. Images are
// downscaled and re-encoded as JPEG; audio is passed through untranscoded
// (wav/mp3 only — the wire formats). Images are decoded and thus
// content-validated; audio bytes are trusted from the caller as-is — only the
// MIME/Kind cross-check applies, the content is never sniffed.
type Part struct {
	Kind PartKind
	Path string // file on disk (extension-routed)
	Data []byte // in-memory bytes (MIME-routed)
	MIME string // required with Data, e.g. "image/png", "audio/mpeg"
}

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
	// Approve resolves guard "ask" verdicts: it blocks the turn goroutine
	// until the host answers (ctx-cancellable — treat cancellation as deny).
	// Nil fails closed: ask degrades to a deny with an explanatory reason.
	Approve func(ctx context.Context, req ApprovalRequest) bool
}

// ErrBusy reports a call that requires the session to be idle while a turn is
// still in flight. Send returns it as an immediate Error event; Clear,
// Rollback, SwitchAgent, and Prune return it (or surface it) directly. Drain
// the in-flight Send channel to completion, then retry.
var ErrBusy = errors.New("shell3: a turn is in flight; drain the Send channel before calling this")

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

	// sink is the JSONL audit log, opened by Start (Spec.OutPath) or
	// Runtime.Session (SessionOpts.OutPath) when the path is non-empty.
	// route writes every internal chat.Event to it (lossless) before
	// translating to a public Event; Close writes the "end" line. nil when no
	// OutPath was configured. sinkCleanup closes the underlying file.
	sink        *chat.OutSink
	sinkCleanup func()

	// runtime and name link a runtime-hosted session back to its registry so
	// Close deregisters it. ownsRuntime marks the single Session that Start
	// creates over a private Runtime: its Close also tears down the shared
	// runtime parts. Start never exposes that Runtime handle, so a competing
	// Runtime.Close can't race the ownsRuntime cleanup.
	runtime     *Runtime
	name        string
	ownsRuntime bool

	// subs holds subagents this session has spawned (see spawn / subRegistry).
	subs subRegistry
	// closeOnce makes Close safe under concurrent invocation: a spawned
	// subagent goroutine calls child.Close() at the same time Runtime.Close may
	// close the same child from its session map. The body runs exactly once;
	// later callers return the recorded error.
	closeOnce sync.Once
	closeErr  error

	// mu guards the current turn's routing target and lifecycle handles.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns
	curDone    <-chan struct{}    // current turn ctx's Done; unblocks a send to an abandoned cur on Close
	turnCancel context.CancelFunc // cancels the in-flight turn (nil before the first Send)
	turnDone   chan struct{}      // closed when the turn goroutine returns (nil before the first Send)
	sawError   bool               // any turn emitted an error event; drives the audit "end" status
	// busy is true from Send until its turn goroutine finishes. It turns a
	// contract violation (overlapping Send/Clear/Rollback/SwitchAgent/Prune,
	// which would race on unsynchronized session state) into ErrBusy instead
	// of a data race.
	busy bool
}

// Start loads the config, builds a single-session Runtime, and returns its one
// Session — the historical single-conversation entry point. Multi-session
// hosts use NewRuntime + Runtime.Session directly. Closing the returned
// Session also closes the underlying Runtime.
func Start(ctx context.Context, spec Spec) (*Session, error) {
	rt, err := NewRuntime(RuntimeSpec{ConfigPath: spec.ConfigPath, WorkDir: spec.WorkDir})
	if err != nil {
		return nil, err
	}
	s, err := rt.Session(SessionOpts{
		Name:             "main",
		Agent:            spec.Agent,
		Headless:         !spec.Interactive,
		ShellInteractive: spec.ShellInteractive,
		// OutPath deliberately empty: Start owns the sink so the start line
		// keeps its historical prompt-derived label (byte-compatible logs).
	})
	if err != nil {
		rt.Close()
		return nil, err
	}
	s.ownsRuntime = true
	if spec.Approve != nil {
		_ = s.SetApprover(spec.Approve) // freshly built session: never busy
	}
	s.cfg.OutPath = spec.OutPath // also feeds writeStartLine's out field (byte-compat) and introspection
	sink, sinkCleanup, err := chat.OpenSink(spec.OutPath)
	if err != nil {
		_ = s.Close() // also closes the runtime via ownsRuntime
		return nil, err
	}
	s.sink, s.sinkCleanup = sink, sinkCleanup
	label := spec.Prompt
	if label == "" {
		label = "(interactive)"
	}
	s.writeStartLine(label)
	return s, nil
}

// writeStartLine writes the audit log's opening line for this session.
// Safe to call regardless of whether a sink was opened: returns immediately
// when s.sink is nil so callers need not guard the call.
func (s *Session) writeStartLine(label string) {
	if s.sink == nil {
		return
	}
	_, model := chat.SplitStatus(s.cfg.StatusLine)
	s.sink.WriteStart(label, s.cfg.ModeLabel, model, s.cfg.OutPath, s.cfg.Headless)
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

// Interject delivers text to the session outside the Send contract: during a
// running turn it is injected at the next round boundary as a system reminder
// ("user interjected …"), letting the model course-correct mid-task; while
// idle it queues and is drained at the start of the next turn. Interject never
// fails, never blocks on a running turn, and is safe to call from any
// goroutine — it is the chat-message path for front-ends (the TUI's
// Enter-while-busy, a bot's incoming message), while Send remains the strict
// turn-starting call.
//
// Optional parts attach media: each invalid part is dropped — Interject never
// fails — and a bracketed "[attachment dropped: <error>]" note is appended to
// the queued text so the drop is visible to both the model and the audit
// reminder.
func (s *Session) Interject(text string, parts ...Part) {
	var cps []llm.ContentPart
	for _, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			text += "\n[attachment dropped: " + err.Error() + "]"
			continue
		}
		cps = append(cps, cp)
	}
	s.sess.Interject(text, cps...)
	// Idle steering must prod the host to run a turn; a busy session's running
	// turn drains the inbox itself, so don't wake (avoids a redundant turn).
	// Benign TOCTOU: isBusy() may flip between this check and the running turn
	// ending — worst case a missed wake (the next Send drains the item anyway)
	// or a spurious wake (RunQueued no-ops on an already-drained inbox). Same
	// reasoning as subagent delivery.
	if !s.isBusy() {
		s.wake()
	}
}

// wake emits a Wake for this session on the runtime bus (no-op without a
// runtime). Reachable from any goroutine via Interject, so it snapshots
// s.runtime under s.mu — mirroring WakeEvents — to avoid racing doClose's nil
// of s.runtime. The lock is not held across emit.
func (s *Session) wake() {
	s.mu.Lock()
	rt := s.runtime
	s.mu.Unlock()
	if rt != nil {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}

// RunQueued runs one turn seeded from the session's queued inbox items — the
// host's response to a Wake event. With an empty inbox (or a turn already in
// flight, which will itself drain the inbox) it returns an already-closed
// channel and starts no turn. Same ErrBusy contract as Send otherwise.
func (s *Session) RunQueued(ctx context.Context) <-chan Event {
	if s.isBusy() || !s.sess.HasInbox() {
		closed := make(chan Event)
		close(closed)
		return closed
	}
	// The turn loop drains the inbox at its top (the reminder + attachments
	// injection point), so an empty-prompt turn consumes the queued items as its
	// initiating input.
	return s.Send(ctx, "")
}

// HasQueuedInput reports whether interjected items are waiting (e.g. steering
// that arrived during a turn's final round). A host can call RunQueued to run a
// turn that consumes them.
func (s *Session) HasQueuedInput() bool { return s.sess.HasInbox() }

// loadPart converts one public Part into an internal ContentPart, enforcing
// the Part contract: exactly one of Path/Data, MIME with Data, and Kind
// matching the loaded media type. Size caps are enforced by the chat loaders.
// Errors are unprefixed: callers add the outermost "shell3: " (loadParts also
// adds the part index; Interject embeds them in a dropped-attachment note).
func (s *Session) loadPart(p Part) (llm.ContentPart, error) {
	if p.Kind != PartImage && p.Kind != PartAudio {
		return llm.ContentPart{}, fmt.Errorf("unknown part kind %s", p.Kind)
	}
	var cp llm.ContentPart
	var err error
	switch {
	case p.Path != "" && len(p.Data) > 0:
		return llm.ContentPart{}, errors.New("part sets both Path and Data; set exactly one")
	case p.Path != "":
		cp, _, err = chat.LoadMediaPart(p.Path, s.cfg.WorkDir)
	case len(p.Data) > 0:
		if p.MIME == "" {
			return llm.ContentPart{}, errors.New("part with Data requires MIME")
		}
		cp, _, err = chat.MediaPartFromBytes(p.Data, p.MIME)
	default:
		return llm.ContentPart{}, errors.New("part sets neither Path nor Data")
	}
	if err != nil {
		return llm.ContentPart{}, err
	}
	want := llm.ContentPartTypeImageURL
	if p.Kind == PartAudio {
		want = llm.ContentPartTypeInputAudio
	}
	if cp.Type != want {
		return llm.ContentPart{}, fmt.Errorf("part declared %s but loaded %s media", p.Kind, cp.Type)
	}
	return cp, nil
}

// loadParts converts a Part slice, failing fast on the first invalid part
// (SendParts' all-or-nothing contract; Interject drops per-part instead).
func (s *Session) loadParts(parts []Part) ([]llm.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]llm.ContentPart, 0, len(parts))
	for i, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			return nil, fmt.Errorf("shell3: part %d: %w", i, err)
		}
		out = append(out, cp)
	}
	return out, nil
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
// and assume exactly one turn is active. The contract is enforced: a Send
// while a turn is in flight does not start a turn — it returns a channel that
// emits a single Error event carrying ErrBusy and closes.
//
// SendParts is the media-carrying variant; Send is SendParts with no parts.
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	return s.SendParts(ctx, prompt, nil)
}

// SendParts runs one turn for prompt with media attachments. Same channel and
// ErrBusy contract as Send. Invalid parts (see Part) reject the whole call:
// the returned channel emits a single Error event carrying the first part's
// error and closes, without starting a turn — the session stays usable.
// Loading happens up front on the caller's goroutine (a Path part reads the
// file here, not on the turn goroutine), and therefore happens even when the
// call is subsequently rejected with ErrBusy.
func (s *Session) SendParts(ctx context.Context, prompt string, parts []Part) <-chan Event {
	cps, err := s.loadParts(parts)
	if err != nil {
		rejected := make(chan Event, 1)
		rejected <- Event{Kind: Error, Err: err}
		close(rejected)
		return rejected
	}
	out := make(chan Event)
	turnCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		cancel()
		rejected := make(chan Event, 1)
		rejected <- Event{Kind: Error, Err: ErrBusy}
		close(rejected)
		return rejected
	}
	s.busy = true
	s.cur = out
	s.curDone = turnCtx.Done()
	s.turnCancel = cancel
	s.turnDone = done
	// Capture the runtime here (under the busy gate, after the ErrBusy
	// early-return) so the turn goroutine doesn't read s.runtime — doClose may
	// nil it concurrently once `done` closes (see the big defer's wake below).
	rt := s.runtime
	s.mu.Unlock()
	tc := s.turnConfig()
	go func() {
		// route forwards events to out during the turn; once the turn returns no
		// further forwarding can happen, so clearing cur, clearing busy, and
		// closing out here is race-free (all run on this goroutine, strictly
		// after the turn).
		defer func() {
			s.mu.Lock()
			if s.cur == out {
				s.cur = nil
			}
			s.busy = false
			s.mu.Unlock()
			close(out)
			// Steering (or a subagent result) that arrived during the turn's final
			// round was queued but never drained — there was no next round boundary.
			// The session is now idle with a non-empty inbox, so Wake the host to run
			// a follow-up turn (RunQueued). Uses the captured rt, not s.runtime, to
			// avoid racing doClose's nil of s.runtime. Emitted after busy is cleared
			// so a host's RunQueued isn't rejected as busy.
			if rt != nil && s.sess.HasInbox() {
				rt.emit(HostEvent{Session: s.name, Kind: Wake})
			}
			cancel() // release the child ctx
		}()
		defer close(done)
		s.sess.RunParts(turnCtx, tc, prompt, cps)
	}()
	return out
}

// SetApprover installs an approval callback for guard "ask" verdicts, adapting
// the public ApprovalRequest type to the internal chat.ApprovalRequest. It may
// be called before the first Send or between turns; the adapter is picked up
// by turnConfig on every subsequent Send. Passing nil removes the approver
// (ask then fails closed). Returns ErrBusy while a turn is in flight: it
// mutates cfg, which the next Send's turnConfig reads (see Send's contract).
func (s *Session) SetApprover(fn func(ctx context.Context, req ApprovalRequest) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	if fn == nil {
		s.cfg.Approve = nil
		return nil
	}
	s.cfg.Approve = func(ctx context.Context, req chat.ApprovalRequest) bool {
		return fn(ctx, ApprovalRequest{
			Tool:    req.Tool,
			RawArgs: req.RawArgs,
			Reason:  req.Reason,
			Agent:   req.Agent,
		})
	}
	return nil
}

// isBusy reports whether a turn is in flight (see Send's contract).
func (s *Session) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// ID returns the store session id (rolls on compaction; "0" with no store).
func (s *Session) ID() string {
	return fmt.Sprintf("%d", s.sess.ID())
}

// Name returns the session's registry name (the value carried in
// HostEvent.Session on the wake bus). Start-created sessions are named "main".
// A host filtering Events() compares HostEvent.Session against this.
func (s *Session) Name() string { return s.name }

// WakeEvents exposes the owning Runtime's out-of-turn event bus (Wake) so a
// single-session front-end created via Start can consume wakes for this session
// without holding a separate *Runtime handle. Returns nil when the session has
// no runtime (e.g. a closed session), in which case a host select on it simply
// never fires. Multi-session hosts should use Runtime.Events() directly.
func (s *Session) WakeEvents() <-chan HostEvent {
	s.mu.Lock()
	rt := s.runtime
	s.mu.Unlock()
	if rt == nil {
		return nil
	}
	return rt.Events()
}

// Close ends the conversation: cancels any in-flight turn, waits for it to
// finish (so its deferred history persist runs against the still-open store),
// then ends the store session and releases the config.
//
// Concurrency: Close must not be called concurrently with itself. A sequential
// second Close is a safe no-op: the turn-cancel and join are idempotent and
// the store, sink, and cleanup paths guard against double execution.
//
// For Start-owned sessions (the common single-session case), Close also tears
// down the private Runtime that Start created — the LLM client, store, MCP
// servers, and proxy spawner. For Runtime-hosted sessions created via
// Runtime.Session, Close deregisters the session from its Runtime (the shared
// parts remain alive for the other sessions).
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
	s.closeOnce.Do(func() { s.closeErr = s.doClose() })
	return s.closeErr
}

// doClose runs the teardown exactly once (guarded by closeOnce in Close).
func (s *Session) doClose() error {
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
	// Capture and nil s.runtime under s.mu so a concurrent WakeEvents() reader
	// (a public accessor bot binaries call) never races this write. Only the
	// field access is locked: rt.forget/rt.cleanup run after the unlock, since
	// holding s.mu across them is unnecessary and could invite a lock-order
	// deadlock with the runtime's own locking.
	s.mu.Lock()
	rt := s.runtime
	s.runtime = nil
	s.mu.Unlock()
	if rt != nil {
		rt.forget(s.name)
		if s.ownsRuntime {
			// Start-owned runtime: no public handle exists, so this is the only
			// place its shared cleanup can run (calling rt.Close() here would
			// re-enter this Close via the session registry).
			rt.cleanup()
		}
	}
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
	cfg := s.cfg
	cfg.Spawn = func(ctx context.Context, req chat.SpawnRequest) (string, error) {
		return s.spawn(ctx, req)
	}
	cfg.ListAgents = func() []chat.AgentSnapshot { return s.subs.snapshot() }
	return chat.NewTurnConfig(cfg, s.handlers, shellInteractive)
}

// Clear resets the conversation context (= /clear): drops all history and
// re-stamps the system prompt with a fresh timestamp. Returns ErrBusy while a
// turn is in flight (see Send's contract).
func (s *Session) Clear() error {
	if s.isBusy() {
		return ErrBusy
	}
	s.sess.SetMessages(nil)
	if s.cfg.RefreshPrompt != nil {
		s.cfg.Personality.SystemPrompt = s.cfg.RefreshPrompt()
	}
	return nil
}

// Rollback drops the last turn from context (= /rollback). ok is false when
// there was nothing to remove. Returns ErrBusy while a turn is in flight (see
// Send's contract).
func (s *Session) Rollback() (ok bool, err error) {
	if s.isBusy() {
		return false, ErrBusy
	}
	msgs := s.sess.Messages()
	pruned := chat.PruneLastTurn(msgs)
	if len(pruned) == len(msgs) {
		return false, nil
	}
	s.sess.SetMessages(pruned)
	return true, nil
}

// SwitchAgent activates the configured agent named name for subsequent Sends
// (= the TUI's /agent <name> or Tab). Switching swaps the agent's model client,
// system prompt, tool set, guard chain, custom-tool routing, skills, status
// line, and context window while keeping conversation history. Returns an error
// for an unknown agent or when the config declares no agents, and ErrBusy
// while a turn is in flight: it mutates cfg in place, which the next Send's
// turnConfig reads (see Send's contract).
func (s *Session) SwitchAgent(name string) error {
	if s.isBusy() {
		return ErrBusy
	}
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
// exists in the conversation, or when a turn is in flight (mutates history;
// see Send's contract).
func (s *Session) Prune(id string) (summary string, ok bool) {
	if s.isBusy() {
		return "error: " + ErrBusy.Error(), false
	}
	msgs := s.sess.Messages()
	out, ok := chat.PruneByID(id, "pruned by user", msgs)
	s.sess.SetMessages(msgs)
	return out, ok
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
