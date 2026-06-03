// Package shell3 embeds the shell3 coding agent as a library. It exposes a
// persistent multi-turn Session (the plugin equivalent of an open TUI) plus a
// one-shot Run convenience, both streaming structured Events. The Session loads
// the same shell3.lua config, store, memory, and persona as the CLI by building
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
		return Event{Kind: Error, Err: errors.New(ev.Text)}, true
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
// completion before the next Send/Clear/Rollback/SwitchModel.
type Session struct {
	cfg       chat.Config
	sess      *chat.Session
	handlers  map[string]chat.ToolHandler
	cleanup   func()
	drainDone chan struct{}

	// mu guards cur, turnCancel, and turnDone.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns (drain clears it on Done/Error)
	turnCancel context.CancelFunc // cancels the in-flight turn; holds the last turn's (idempotent) cancel between turns
	turnDone   chan struct{}      // closed when the turn goroutine returns; holds the last turn's (already-closed) channel between turns
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
	})
	if err != nil {
		return nil, err
	}
	return newSession(cfg, cleanup), nil
}

// newSession wires a Session around an already-built chat.Config and starts the
// drain. Split out from Start so tests can inject a fakellm-backed config.
func newSession(cfg chat.Config, cleanup func()) *Session {
	var storeID int64
	if cfg.Store != nil {
		if id, err := cfg.Store.StartSession(); err == nil {
			storeID = id
		}
	}
	sess := chat.NewSession(chat.SessionOpts{
		BufSize:          256,
		StoreID:          storeID,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
	})
	s := &Session{
		cfg:       cfg,
		sess:      sess,
		handlers:  chat.NewHandlers(cfg),
		cleanup:   cleanup,
		drainDone: make(chan struct{}),
	}
	go s.drain()
	return s
}

// drain is the single long-lived consumer of sess.Events(), routing translated
// events to the current Send channel and closing it on Done/Error.
func (s *Session) drain() {
	defer close(s.drainDone)
	for ev := range s.sess.Events() {
		pub, ok := translate(ev)
		if !ok {
			continue
		}
		s.mu.Lock()
		cur := s.cur
		s.mu.Unlock()
		if cur == nil {
			continue
		}
		cur <- pub
		if pub.Kind == Done || pub.Kind == Error {
			// Close the captured local, not a re-read of s.cur: a misbehaving
			// caller could repoint s.cur via an early Send between the send
			// above and here, and we must never close that fresh channel.
			s.mu.Lock()
			close(cur)
			if s.cur == cur {
				s.cur = nil
			}
			s.mu.Unlock()
		}
	}
}

// Send runs one turn for prompt and returns a channel of that turn's events,
// closed after the turn's Done (or Error).
//
// Single-turn-at-a-time contract: the caller MUST drain the returned channel
// to completion before calling Send, Clear, Rollback, or SwitchModel again.
// Those methods read and mutate unsynchronized session state (messages, cfg)
// and assume exactly one turn is active; overlapping them is a data race, not
// a supported concurrency mode.
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	out := make(chan Event)
	turnCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	s.cur = out
	s.turnCancel = cancel
	s.turnDone = done
	s.mu.Unlock()
	tc := s.turnConfig()
	go func() {
		defer close(done)
		defer cancel() // release the child ctx when the turn ends
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
// then stops the drain, ends the store session, and releases the config.
//
// As with the other lifecycle methods, the caller must not abandon an in-flight
// Send channel: if events go unread, the drain goroutine parks on the unbuffered
// channel send and Close blocks at <-drainDone. Drain the Send channel (Close
// cancels the turn, so an in-flight turn ends promptly with a terminal event).
func (s *Session) Close() error {
	// Cancel any in-flight turn so it stops streaming and runs its deferred
	// history persist, then join it before we close the events channel — the
	// turn's terminal emitSync needs the drain goroutine (events channel still
	// open) to receive it, so we must wait for <-done before CloseEvents.
	// Joining before CloseEvents also avoids a race between close(s.events)
	// and a concurrent s.events<-ev inside emitSync.
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
	s.sess.CloseEvents()
	<-s.drainDone
	if s.cfg.Store != nil {
		_ = s.cfg.Store.EndSession(s.sess.ID())
	}
	s.cleanup()
	return nil
}

// turnConfig derives the per-turn config from the current cfg. Built fresh each
// turn so SwitchModel's mutations to cfg take effect on the next Send.
func (s *Session) turnConfig() chat.TurnConfig {
	return chat.TurnConfig{
		LLM:             s.cfg.LLM,
		Personality:     s.cfg.Personality,
		StatusLine:      s.cfg.StatusLine,
		WorkDir:         s.cfg.WorkDir,
		Store:           s.cfg.Store,
		Truncate:        s.cfg.Truncate,
		Handlers:        s.handlers,
		Log:             chat.LogOrNoop(s.cfg.Log),
		Headless:        true,
		CustomTool:      s.cfg.CustomTool,
		CustomToolNames: s.cfg.CustomToolNames,
		ToolGuard:       s.cfg.ToolGuard,
		ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in plugin mode"
		},
	}
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

// SwitchModel activates the configured model named name for subsequent Sends
// (= /model <name>). Returns an error for an unknown model or when the config
// declares no models. Call only between turns: it mutates cfg in place, which
// the next Send's turnConfig reads, so drain any in-flight Send channel first
// (see Send's contract).
func (s *Session) SwitchModel(name string) error {
	if s.cfg.SwitchModel == nil {
		return fmt.Errorf("no models configured")
	}
	am, err := s.cfg.SwitchModel(name)
	if err != nil {
		return err
	}
	s.cfg.LLM = am.Client
	s.cfg.Params = am.Params
	s.cfg.ContextWindow = am.ContextWindow
	s.cfg.StatusLine = fmt.Sprintf("%s │ %s", s.cfg.ModeLabel, am.ModelID)
	return nil
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
