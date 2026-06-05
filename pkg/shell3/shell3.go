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
	"sync"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// Spec configures Run / Start. Prompt is used by Run only.
type Spec struct {
	Prompt     string
	ConfigPath string // "" → ./shell3.lua then ~/.shell3/shell3.lua
	WorkDir    string // "" → os.Getwd()
	Agent      string // "" → first declared agent; unknown name fails Start/Run
}

// Kind discriminates a streamed Event.
type Kind int

const (
	Token      Kind = iota // assistant text       → Text
	Reasoning              // thinking text         → Text
	ToolCall               // tool started          → ToolName, ToolInput
	ToolResult             // tool finished         → ToolName, ToolOutput
	Usage                  // per-roundtrip tokens  → PromptTokens/CompletionTokens/TotalTokens
	Retry                  // transient retry       → Text
	Error                  // turn error            → Err
	Done                   // turn end (normal)     → token fields (final totals)
)

// Event is one item streamed on a Send/Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind             Kind
	Text             string // Token, Reasoning, Retry
	ToolName         string // ToolCall, ToolResult
	ToolInput        string // ToolCall (raw JSON args)
	ToolOutput       string // ToolResult
	PromptTokens     int    // Usage, Done
	CompletionTokens int    // Usage, Done
	TotalTokens      int    // Usage, Done
	Err              error  // Error
}

// translate maps an internal chat.Event to a public Event. ok is false when the
// internal event has no public equivalent (session lifecycle, echoed user
// message, post-stream assistant message, injected reminders).
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventAssistantReasoning:
		return Event{Kind: Reasoning, Text: ev.Text}, true
	case chat.EventToolCall:
		return Event{Kind: ToolCall, ToolName: ev.ToolName, ToolInput: ev.ToolInput}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput}, true
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

	// mu guards the current turn's routing target and lifecycle handles.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns
	curDone    <-chan struct{}    // current turn ctx's Done; unblocks a send to an abandoned cur on Close
	turnCancel context.CancelFunc // cancels the in-flight turn (nil before the first Send)
	turnDone   chan struct{}      // closed when the turn goroutine returns (nil before the first Send)
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
		Headless:   true,
		Agent:      spec.Agent,
	})
	if err != nil {
		return nil, err
	}
	return newSession(cfg, cleanup), nil
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
func (s *Session) route(ev chat.Event) {
	pub, ok := translate(ev)
	if !ok {
		return
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
// closed after the turn's Done (or Error).
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
		// route forwards events to out during the turn; once Run returns no
		// further forwarding can happen, so clearing cur and closing out here
		// is race-free (both run on this goroutine, strictly after Run).
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
	s.cleanup()
	return endErr
}

// turnConfig derives the per-turn config from the current cfg. Built fresh each
// turn so SwitchAgent's mutations to cfg take effect on the next Send.
func (s *Session) turnConfig() chat.TurnConfig {
	return chat.NewTurnConfig(s.cfg, s.handlers, func(ctx context.Context, cmd, workdir string) string {
		return "error: interactive TTY not available in plugin mode"
	})
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
