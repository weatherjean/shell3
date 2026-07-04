package acp

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// acpAgent implements acp.Agent for shell3. It owns the session registry and
// holds a reference to the AgentSideConnection so later tasks can push updates.
type acpAgent struct {
	rt   *shell3.Runtime
	conn *acpsdk.AgentSideConnection
	opts Options

	mu       sync.Mutex
	byID     map[string]*acpSession // ACP sessionId → session
	byName   map[string]*acpSession // runtime name → session
	clientFS bool                   // client advertised fs.readTextFile && fs.writeTextFile
}

func newACPAgent(rt *shell3.Runtime, opts Options) *acpAgent {
	return &acpAgent{
		rt:     rt,
		opts:   opts,
		byID:   map[string]*acpSession{},
		byName: map[string]*acpSession{},
	}
}

// agentVersion returns the module version from build info, or "dev".
func agentVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

// sessionByID returns the acpSession for the given ACP session ID, or nil.
func (a *acpAgent) sessionByID(id string) *acpSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.byID[id]
}

// sessionByName returns the acpSession for the given runtime registry name, or
// nil. HostEvent.Session carries the registry name (Session.Name()), so the pump
// resolves out-of-turn events through this map. nil means the event names a
// session this front-end does not own (a child session, or one owned by another
// front-end) — the pump skips it.
func (a *acpAgent) sessionByName(name string) *acpSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.byName[name]
}

// ── ACP method implementations ──────────────────────────────────────────────

// Initialize handles the ACP initialize handshake, negotiating the protocol
// version and advertising agent capabilities.
func (a *acpAgent) Initialize(_ context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	// Protocol version negotiation: respond with ProtocolVersionNumber (=1) if the
	// client supports >= 1; otherwise echo the client's value so it can disconnect.
	version := params.ProtocolVersion
	if version >= acpsdk.ProtocolVersionNumber {
		version = acpsdk.ProtocolVersionNumber
	}
	// Record whether the client supports the full fs capability (both read and
	// write required). Sessions created after this point will use the ACP
	// editor-buffer backend instead of the OS backend when clientFS is true.
	a.mu.Lock()
	a.clientFS = params.ClientCapabilities.Fs.ReadTextFile && params.ClientCapabilities.Fs.WriteTextFile
	a.mu.Unlock()
	return acpsdk.InitializeResponse{
		ProtocolVersion: version,
		AgentInfo: &acpsdk.Implementation{
			Name:    "shell3",
			Version: agentVersion(),
		},
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: acpsdk.SessionCapabilities{
				List:   &acpsdk.SessionListCapabilities{},
				Close:  &acpsdk.SessionCloseCapabilities{},
				Resume: &acpsdk.SessionResumeCapabilities{},
			},
			PromptCapabilities: acpsdk.PromptCapabilities{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
		},
		// AuthMethods must be non-nil (empty slice, not nil) per the protocol spec.
		AuthMethods: []acpsdk.AuthMethod{},
	}, nil
}

// askerFor returns a shell3 Asker func that forwards on_tool_call ask-verdicts
// to the ACP client as session/request_permission requests.
//
// sessionID is a pointer because the Asker must be constructed BEFORE rt.Session()
// returns the session ID. Callers create id := new(string), pass it here, then
// fill *id = sess.ID() after Session() returns. A tool cannot fire before
// NewSession returns, so *id is always populated by the time the asker is called.
//
// conn is captured under a.mu on each call (never held across the blocking
// RequestPermission RPC) so the race detector stays clean.
//
// ToolCallId: the ask fires while a tool call is executing, AFTER its
// tool_call card was streamed to the client, so the request reuses the REAL
// streamed ToolCallID (tracked by acpSession.noteToolEvent) — clients that
// materialize the embedded ToolCallUpdate (Zed) then attach the permission to
// the existing card instead of rendering a dangling duplicate. When the real
// id is unknown (asker fired outside a tracked turn, or the hand-off race
// window in noteToolEvent) it falls back to a synthetic "perm-N" id and, after
// resolution, sends a terminal tool_call_update for it so the synthetic card
// never lingers as forever-pending.
//
// Fail-closed: every path that is not a clean "selected == allow" returns false.
func (a *acpAgent) askerFor(sessionID *string) func(ctx context.Context, command, reason string) bool {
	var seq atomic.Int64
	return func(ctx context.Context, command, reason string) bool {
		title := command
		if reason != "" {
			title = fmt.Sprintf("%s — %s", command, reason)
		}

		// The disable_safety auto-allow lives in the shell3.Session's Asker
		// wrapper (Session.SetSafetyOff) — by the time this fires, the toggle
		// has already been honored. Looked up by id because the acpSession does
		// not exist yet when this closure is built; the lookup yields the real
		// in-flight ToolCallID when available.
		toolCallID := ""
		if s := a.sessionByID(*sessionID); s != nil {
			toolCallID = s.liveToolCallWait(100 * time.Millisecond)
		}
		synthetic := toolCallID == ""
		if synthetic {
			toolCallID = fmt.Sprintf("perm-%d", seq.Add(1))
		}

		// Read conn under the lock; then release before the blocking RPC.
		a.mu.Lock()
		conn := a.conn
		a.mu.Unlock()
		if conn == nil {
			return false
		}

		resp, err := conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
			SessionId: acpsdk.SessionId(*sessionID),
			ToolCall: acpsdk.ToolCallUpdate{
				ToolCallId: acpsdk.ToolCallId(toolCallID),
				Title:      acpsdk.Ptr(title),
				Kind:       acpsdk.Ptr(acpsdk.ToolKindExecute),
				Status:     acpsdk.Ptr(acpsdk.ToolCallStatusPending),
			},
			Options: []acpsdk.PermissionOption{
				{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
				{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
			},
		})
		// Allow only when the user explicitly selected the "allow" option.
		// Cancelled, reject-selected, RPC error, or any unknown outcome → deny
		// (fail-closed).
		allowed := err == nil && resp.Outcome.Selected != nil &&
			resp.Outcome.Selected.OptionId == acpsdk.PermissionOptionId("allow")

		// A synthetic id has no real tool-call lifecycle behind it, so terminate
		// the card the RequestPermission may have materialized. A real id needs
		// no update here: the tool's own tool_call_update (completed, or failed
		// for a blocked run) closes the card through the normal stream.
		if synthetic {
			status := acpsdk.ToolCallStatusFailed
			if allowed {
				status = acpsdk.ToolCallStatusCompleted
			}
			_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: acpsdk.SessionId(*sessionID),
				Update: acpsdk.UpdateToolCall(
					acpsdk.ToolCallId(toolCallID),
					acpsdk.WithUpdateStatus(status),
				),
			})
		}
		return allowed
	}
}

// modeState builds the ACP SessionModeState for a shell3 session from its
// configured agent list and active agent. Returns nil when no agents are
// configured (the session has no mode to advertise).
func modeState(sess *shell3.Session) *acpsdk.SessionModeState {
	names := sess.AgentNames()
	if len(names) == 0 {
		return nil
	}
	modes := make([]acpsdk.SessionMode, 0, len(names))
	for _, n := range names {
		modes = append(modes, acpsdk.SessionMode{Id: acpsdk.SessionModeId(n), Name: n})
	}
	return &acpsdk.SessionModeState{
		CurrentModeId:  acpsdk.SessionModeId(sess.ActiveAgent()),
		AvailableModes: modes,
	}
}

// NewSession creates a new shell3 session for the ACP client.
func (a *acpAgent) NewSession(_ context.Context, params acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	// idPtr is pre-allocated so askerFor can capture it before rt.Session()
	// returns the actual session ID. The Asker cannot fire before NewSession
	// returns, so *idPtr is always filled by the time the first tool call runs.
	//
	// acpFS also holds idPtr so it can lazily dereference the session ID at
	// call time — the ID is only known after rt.Session returns, but acpFS
	// must be constructed before that call to be passed in SessionOpts.
	idPtr := new(string)
	a.mu.Lock()
	useFS := a.clientFS
	conn := a.conn
	a.mu.Unlock()
	var fsBackend shell3.FileSystem // nil → OS default
	if useFS && conn != nil {
		fsBackend = acpFS{conn: conn, sessionID: idPtr}
	}
	sess, err := a.rt.Session(shell3.SessionOpts{
		Agent:    a.opts.DefaultAgent,
		WorkDir:  params.Cwd,
		Headless: true,
		Asker:    a.askerFor(idPtr),
		FS:       fsBackend,
	})
	if err != nil {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(err.Error())
	}
	*idPtr = sess.ID() // fill after creation; safe because no tool can run before this
	id := acpsdk.SessionId(sess.ID())
	as := newACPSession(string(id), params.Cwd, sess)
	a.mu.Lock()
	a.byID[string(id)] = as
	a.byName[sess.Name()] = as
	a.mu.Unlock()
	// Advertise commands AFTER this response reaches the client: the client
	// only learns this session's id from the NewSessionResponse below, so a
	// notification sent before it (synchronously here) references an unknown
	// session and is dropped. The goroutine lands after the SDK writes the
	// response; Prompt re-advertises as a guaranteed backstop.
	go a.advertiseCommands(string(id))
	return acpsdk.NewSessionResponse{
		SessionId: id,
		Modes:     modeState(sess),
	}, nil
}

// ── Non-session methods ───────────────────────────────────────────────────────

// Authenticate: no authentication methods are configured.
func (a *acpAgent) Authenticate(_ context.Context, _ acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, acpsdk.NewInvalidParams("no authentication methods configured")
}

// Logout: not supported.
func (a *acpAgent) Logout(_ context.Context, _ acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, acpsdk.NewInvalidRequest("logout is not supported")
}

// Cancel calls the per-turn cancel func if one is set, then returns nil.
// Unknown or already-idle sessions are silently ignored.
func (a *acpAgent) Cancel(_ context.Context, params acpsdk.CancelNotification) error {
	s := a.sessionByID(string(params.SessionId))
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancelTurn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// ── Session lifecycle ─────────────────────────────────────────────────────────

// CloseSession cancels any in-flight turn, closes the underlying shell3 session,
// and deregisters it from the agent's registry. Returns InvalidParams for an
// unknown session id.
func (a *acpAgent) CloseSession(_ context.Context, params acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	id := string(params.SessionId)
	a.mu.Lock()
	s := a.byID[id]
	a.mu.Unlock()
	if s == nil {
		return acpsdk.CloseSessionResponse{}, acpsdk.NewInvalidParams(map[string]any{"sessionId": id})
	}

	// Cancel any in-flight turn before closing the session.
	s.mu.Lock()
	cancel := s.cancelTurn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	// Close the underlying shell3 session (ends the store record + releases config).
	_ = s.sess.Close()

	// Deregister from both maps.
	a.mu.Lock()
	delete(a.byID, s.id)
	delete(a.byName, s.sess.Name())
	a.mu.Unlock()

	return acpsdk.CloseSessionResponse{}, nil
}

// ListSessions maps rt.PastSessions to ACP SessionInfo records.
//
// UpdatedAt is the LastAt field from SessionMeta, already in RFC3339 (ISO 8601)
// format, so it is passed directly as a *string pointer.
func (a *acpAgent) ListSessions(_ context.Context, _ acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	metas, err := a.rt.PastSessions(100)
	if err != nil {
		return acpsdk.ListSessionsResponse{}, acpsdk.NewInternalError(err.Error())
	}
	sessions := make([]acpsdk.SessionInfo, 0, len(metas))
	for _, m := range metas {
		info := acpsdk.SessionInfo{
			SessionId: acpsdk.SessionId(m.ID),
			Cwd:       "", // SessionMeta does not expose Workdir
		}
		if m.Preview != "" {
			info.Title = acpsdk.Ptr(m.Preview)
		}
		if m.LastAt != "" {
			// LastAt is already RFC3339 (= ISO 8601); cast directly to *string.
			info.UpdatedAt = acpsdk.Ptr(m.LastAt)
		}
		sessions = append(sessions, info)
	}
	return acpsdk.ListSessionsResponse{Sessions: sessions}, nil
}

// resumeAndRegister is the shared core of LoadSession and ResumeSession. It:
//  1. Validates the session id exists in the runs store (InvalidParams if not).
//  2. Guards against re-opening an already-registered session (InvalidRequest) —
//     see below.
//  3. Resumes the session via rt.Session(ResumeID=id) and registers it under
//     byID/byName, using the pointer-id trick so askerFor can capture the ID
//     before rt.Session returns.
//
// It returns the registered *acpSession plus the session's stored history
// entries. Those entries are used by LoadSession to emit replay updates;
// ResumeSession ignores them (they are nil for the PastSessions-only fallback
// path, which is fine — a resume never replays).
//
// Double-open guard (fail-closed): opening a second *shell3.Session against a
// runs-store id that is already open would mint an independent busy guard on the
// SAME store record (interleaved JSONL writes = logical corruption), overwrite
// byID[id] (leaking the old session, whose cancelTurn becomes unreachable), and
// orphan the old byName entry. So if the id is already registered we return
// InvalidRequest rather than the existing session — returning the existing one
// would let a re-load wrongly replay history a second time.
//
// The byID guard read and the byID/byName registration are both done under a.mu;
// the lock is never held across the blocking rt.Session call. Note the guard
// closes the sequential re-open case (load, then load the same id again later);
// a concurrent same-id load is out of scope — ACP clients issue loads
// sequentially per connection and await each response.
func (a *acpAgent) resumeAndRegister(id, cwd string) (*acpSession, []shell3.HistoryEntry, error) {
	// Validate: session must be known (has messages, or appears in PastSessions).
	entries, err := a.rt.SessionMessages(id)
	if err != nil {
		entries = nil // treat store errors as "not found" here; re-check below
	}
	if len(entries) == 0 {
		metas, merr := a.rt.PastSessions(10000)
		if merr != nil {
			return nil, nil, acpsdk.NewInternalError(merr.Error())
		}
		found := false
		for _, m := range metas {
			if m.ID == id {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, acpsdk.NewInvalidParams(map[string]any{"sessionId": id})
		}
	}

	// Double-open guard: fail closed if this id is already open (see doc comment).
	a.mu.Lock()
	existing := a.byID[id]
	a.mu.Unlock()
	if existing != nil {
		return nil, nil, acpsdk.NewInvalidRequest(map[string]any{"error": "session already open", "sessionId": id})
	}

	// Resume the session; idPtr allows askerFor to capture the ID before rt.Session returns.
	// acpFS holds the same idPtr so it can lazily dereference the session ID at call time.
	idPtr := new(string)
	a.mu.Lock()
	useFS := a.clientFS
	conn := a.conn
	a.mu.Unlock()
	var fsBackend shell3.FileSystem // nil → OS default
	if useFS && conn != nil {
		fsBackend = acpFS{conn: conn, sessionID: idPtr}
	}
	sess, err := a.rt.Session(shell3.SessionOpts{
		ResumeID: id,
		WorkDir:  cwd,
		Headless: true,
		Asker:    a.askerFor(idPtr),
		FS:       fsBackend,
	})
	if err != nil {
		return nil, nil, acpsdk.NewInternalError(err.Error())
	}
	*idPtr = sess.ID()
	as := newACPSession(sess.ID(), cwd, sess)
	a.mu.Lock()
	a.byID[sess.ID()] = as
	a.byName[sess.Name()] = as
	a.mu.Unlock()
	return as, entries, nil
}

// LoadSession satisfies acp.AgentLoader. It resumes+registers the session via
// resumeAndRegister (which also enforces the double-open guard), then emits all
// replay updates (history replay) to the ACP client via conn.SessionUpdate
// BEFORE returning — because the transport is an ordered io.Pipe, the client
// receives every notification before it unblocks from conn.LoadSession(). That
// is the replay-ordering guarantee: the emits happen before the handler returns.
//
// conn is copied under a.mu before the emit loop (same pattern as Prompt/askerFor)
// so the race detector stays clean. context.Background() is used for emits so a
// cancelled request context cannot drop replay notifications.
func (a *acpAgent) LoadSession(_ context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	as, entries, err := a.resumeAndRegister(string(params.SessionId), params.Cwd)
	if err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}

	// Copy conn under the lock; release before the blocking SessionUpdate calls.
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	// Emit ALL replay updates BEFORE returning the response (the ordering guarantee).
	if conn != nil && len(entries) > 0 {
		for _, u := range replayUpdates(entries) {
			_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: params.SessionId,
				Update:    u,
			})
		}
	}

	// Advertise commands AFTER replay so updates arrive in a sensible order:
	// replay history → commands menu → response.
	go a.advertiseCommands(as.id)

	return acpsdk.LoadSessionResponse{Modes: modeState(as.sess)}, nil
}

// ResumeSession resumes an existing session without replaying history.
// Same flow as LoadSession (via resumeAndRegister) minus the replay-update emit
// step; the returned history entries are intentionally ignored.
func (a *acpAgent) ResumeSession(_ context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	as, _, err := a.resumeAndRegister(string(params.SessionId), params.Cwd)
	if err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	go a.advertiseCommands(as.id)
	return acpsdk.ResumeSessionResponse{Modes: modeState(as.sess)}, nil
}

// Prompt streams a user turn through the shell3 session and forwards events to
// the ACP client. It blocks until the turn completes, is cancelled, or errors.
func (a *acpAgent) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	s := a.sessionByID(string(params.SessionId))
	if s == nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"sessionId": params.SessionId})
	}
	// Re-advertise commands for this (already client-known) session, so the
	// slash-command menu is populated even if the post-NewSession notification
	// was dropped. Cheap and self-healing; the session id is known here so
	// there is no ordering race with a session-creation response.
	a.advertiseCommands(s.id)
	text, parts, err := promptToParts(params.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
	}

	// Command interception: if the prompt is exactly a registered slash command
	// (e.g. "/clear"), handle it locally without invoking the LLM.
	if cmd, ok := matchCommand(text); ok {
		reply, cmdErr := cmd.run(s)
		if cmdErr != nil {
			reply = "error: " + cmdErr.Error()
		}
		// Emit the reply as an agent message. conn is read under the lock here
		// (never held across the blocking SessionUpdate call).
		a.mu.Lock()
		cmdConn := a.conn
		a.mu.Unlock()
		if cmdConn != nil {
			_ = cmdConn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: acpsdk.SessionId(s.id),
				Update:    acpsdk.UpdateAgentMessageText(reply),
			})
		}
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}

	// Capture conn under the lock — it is set once in Run before any requests
	// arrive, but the race detector requires explicit synchronization.
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInternalError(map[string]any{"error": "no client connection"})
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Claim the session's single turn slot, WAITING for any in-flight turn (a
	// previous client Prompt or a Wake-driven drain) to unwind first. This is
	// how ACP prompt supersede works end-to-end: when the client sends a new
	// session/prompt while one is running, the SDK cancels the previous
	// prompt's request ctx — the old turn unwinds (its Prompt returns
	// stopReason cancelled) and releases the slot, and this prompt takes over.
	// The wait is bounded by this request's own ctx, which the SDK cancels in
	// turn if THIS prompt is itself superseded or the connection tears down.
	if err := s.acquireTurn(ctx, cancel); err != nil {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	defer s.releaseTurn()

	ctxWindow := s.sess.Snapshot().ContextWindow
	var turnErr error
	for {
		turnErr = nil
		for ev := range s.sess.SendParts(turnCtx, text, parts) {
			switch ev.Kind {
			case shell3.Error:
				turnErr = ev.Err
			default:
				s.forward(ctx, conn, ev, ctxWindow)
			}
		}
		if !errors.Is(turnErr, shell3.ErrBusy) {
			break
		}
		// Defensive: holding the turn slot means no other ACP-driven turn can be
		// in flight (shell3 clears busy before closing the event channel, so the
		// previous slot owner's turn had fully unwound before we acquired). If a
		// host-side caller still had the session busy, wait briefly and retry,
		// bounded by the request ctx (cancelled on supersede/cancel).
		select {
		case <-ctx.Done():
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		case <-time.After(20 * time.Millisecond):
		}
	}
	switch {
	case turnCtx.Err() != nil || errors.Is(turnErr, context.Canceled):
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	case turnErr != nil:
		// Append the rollback affordance when the error looks recoverable by
		// undoing the last turn (matches the headless front-end's presentation).
		msg := turnErr.Error()
		if hint := shell3.RollbackHint(turnErr); hint != "" {
			msg += " " + hint
		}
		return acpsdk.PromptResponse{}, acpsdk.NewInternalError(map[string]any{"error": msg})
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

// SetSessionConfigOption: not supported.
func (a *acpAgent) SetSessionConfigOption(_ context.Context, _ acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidRequest("session config options not supported")
}

// SetSessionMode switches the active agent of an existing session to the
// named mode (= agent). On success, a current_mode_update notification is
// emitted so the client can update its UI without polling.
//
// Error mapping:
//   - unknown session or unknown modeId → InvalidParams
//   - session busy (ErrBusy)            → InvalidRequest
//   - other failure                     → InternalError
//
// conn is copied out under a.mu and released before calling SessionUpdate to
// avoid holding the lock across a blocking network call (mirrors Prompt/askerFor).
func (a *acpAgent) SetSessionMode(ctx context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	s := a.sessionByID(string(params.SessionId))
	if s == nil {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidParams(map[string]any{"sessionId": params.SessionId})
	}

	// Pre-check membership via AgentNames so that unknown-agent errors can be
	// reported as InvalidParams rather than InternalError — SwitchAgent returns
	// a plain fmt.Errorf for unknown agents, not a distinguishable sentinel.
	modeID := string(params.ModeId)
	known := false
	for _, n := range s.sess.AgentNames() {
		if n == modeID {
			known = true
			break
		}
	}
	if !known {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidParams(map[string]any{"modeId": params.ModeId})
	}

	if err := s.sess.SwitchAgent(modeID); err != nil {
		if errors.Is(err, shell3.ErrBusy) {
			return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidRequest(map[string]any{"error": "a turn is already in flight"})
		}
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	// Emit current_mode_update. Copy conn under the lock, release before calling
	// SessionUpdate — mirrors the pattern in Prompt/askerFor so the race
	// detector stays clean. Use context.Background() (consistent with forward)
	// so a cancelled request ctx cannot drop the notification.
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn != nil {
		_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(s.id),
			Update: acpsdk.SessionUpdate{
				CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{
					CurrentModeId: params.ModeId,
				},
			},
		})
	}
	return acpsdk.SetSessionModeResponse{}, nil
}
