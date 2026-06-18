package shell3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
)

// TelegramConfig mirrors the parsed shell3.telegram{} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Agent     string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig mirrors the parsed shell3.telegram.dashboard{} block.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}

// CronJob mirrors one parsed cron job (shell3.telegram cron list).
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// RuntimeSpec configures a long-lived Runtime: the process-wide unit owning
// the config (Lua state), store, proxy spawner, and log.
type RuntimeSpec struct {
	ConfigPath string // "" → ~/.shell3/shell3.lua
	WorkDir    string // runtime root; "" → os.Getwd(). Sessions default here.
	// Context, when non-nil, parents the runtime's base context: cancelling it
	// tears down the runtime (and any in-flight session/turn) just as Close
	// does. "" → context.Background() (lifetime bounded only by Close).
	Context context.Context
}

// SessionOpts parameterizes one Session on a Runtime.
type SessionOpts struct {
	// Name keys the session on the runtime (e.g. "tg:1234"). "" gets a unique
	// generated name. Requesting an existing live name returns that session.
	Name string
	// Agent selects the initial agent ("" → first declared).
	Agent string
	// WorkDir roots tool execution for this session ("" → runtime root).
	WorkDir string
	// Headless strips shell_interactive and injects the headless reminder.
	Headless bool
	// OutPath, when non-empty, streams this session's JSONL audit log there.
	OutPath string
	// ShellInteractive runs an interactive shell command with TTY access.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
	// Asker confirms a bash_safety ask-verdict command with a human (true = allow).
	Asker func(ctx context.Context, command, reason string) bool
	// ResumeID reloads a stored session's messages when non-empty.
	ResumeID string
	// ResumeLatest reattaches to the newest stored session matching this
	// session's workdir+config (instead of starting fresh) when ResumeID is empty.
	// Falls back to a new session when none exists. The Telegram bot sets this so
	// a restart rejoins the live conversation rather than spawning empties.
	ResumeLatest bool
	// ParentSession is the report pointer written to the new session row. When
	// non-empty this run is a subagent: on completion it appends one pointer line
	// to the project inbox for the live host to surface.
	ParentSession string
	// ReportInbox, when non-empty, is the absolute inbox.jsonl path this run
	// reports completion to instead of its own store inbox — the parent's inbox,
	// so delivery is independent of the subagent's working directory.
	ReportInbox string
}

// HostEventKind enumerates out-of-turn runtime events.
type HostEventKind int

const (
	// Wake signals a session's inbox gained an item while no turn was running.
	// The host should call Session.RunQueued to react (runs a model turn).
	Wake HostEventKind = iota
	// Notice is a ready-to-display message for the session's chat (e.g. a cron /
	// host-dispatch result). The host shows Text verbatim; it is NOT a turn and
	// never touches the agent's inbox or context.
	Notice
)

// HostEvent is one out-of-turn event for a session. Notice carries Text; Wake
// carries only the session name.
type HostEvent struct {
	Session string
	Kind    HostEventKind
	Text    string
}

// Runtime hosts N sessions over one shared build. Create with NewRuntime,
// release with Close. Safe for concurrent Session calls.
//
// Lifetime: NewRuntime → Session (×N) → Close. Close is idempotent; any
// sessions still open at Close time are closed first. Sessions deregister
// from the runtime automatically on their own Close.
type Runtime struct {
	// sessionConfig derives a per-session chat.Config; production wires
	// agentsetup.Parts.SessionConfig, tests inject fakes.
	sessionConfig func(SessionOpts) (chat.Config, error)
	// subagentDesc returns a registered subagent's model-facing description (and
	// whether it exists). The Session uses it to render the delegation context's
	// "name: description" allowlist. nil in tests that don't exercise delegation.
	subagentDesc func(name string) (string, bool)
	cleanup      func()

	// events is the out-of-turn event bus (Wake). Buffered; emit drops on full.
	events chan HostEvent
	// workDir is the runtime root; subagents write audit logs under it.
	workDir string
	// store is the shared file-native runs store (nil if unavailable). Used by
	// PastSessions/SessionTurns for the dashboard's conversation history and by
	// the inbox watcher that surfaces subagent completions.
	store *runs.Store
	// ctx/cancel scope host-initiated cron dispatch subprocesses so a running
	// cron job is cancelled (and its goroutine unblocks) when Close cancels ctx.
	ctx    context.Context
	cancel context.CancelFunc

	telegram TelegramConfig
	cron     []CronJob

	configPath string // captured from RuntimeSpec for Reload
	homeDir    string // captured for Reload's BuildParts

	// subSeq mints process-unique ids for cron dispatch transcripts (see
	// nextSubID). Global to the runtime so concurrent cron fires never collide on
	// a transcript filename.
	subSeq atomic.Int64
	// wg joins in-flight cron dispatch goroutines at Close. Add happens only
	// under rt.mu while !closed (see trackSubagent), so it can't race wg.Wait.
	wg sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool
}

// nextSubID returns a process-unique id for this runtime, used as a cron
// dispatch transcript filename stem.
func (rt *Runtime) nextSubID() string {
	return fmt.Sprintf("a%d", rt.subSeq.Add(1))
}

// trackSubagent starts fn as a tracked goroutine (a cron dispatch), unless the
// runtime is already closing. Returns false if the runtime is closed (caller
// should not have started the work). Add happens under rt.mu so it cannot race
// Close's transition to closed + wg.Wait.
func (rt *Runtime) trackSubagent(fn func()) bool {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return false
	}
	rt.wg.Add(1)
	rt.mu.Unlock()
	go func() { defer rt.wg.Done(); fn() }()
	return true
}

// NewRuntime loads the config and assembles the shared runtime parts.
// The Runtime must be Closed; sessions left open are closed by Close.
func NewRuntime(spec RuntimeSpec) (*Runtime, error) {
	parent := spec.Context
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, err // caller already cancelled — don't build a runtime
	}
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
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: spec.ConfigPath, CWD: workDir, HomeDir: homeDir,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	tg := parts.Telegram()
	var cronJobs []CronJob
	for _, j := range parts.Cron() {
		cronJobs = append(cronJobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
			})
		},
		subagentDesc: parts.SubagentDescription,
		cleanup:      cleanup,
		store:        parts.Store(),
		telegram: TelegramConfig{
			Token:   tg.Token,
			ChatID:  tg.ChatID,
			Agent:   tg.Agent,
			WorkDir: tg.WorkDir,
			Dashboard: DashboardConfig{
				Enabled: tg.Dashboard.Enabled,
				Addr:    tg.Dashboard.Addr,
				URL:     tg.Dashboard.URL,
			},
		},
		cron:       cronJobs,
		events:     make(chan HostEvent, 64),
		workDir:    workDir,
		configPath: spec.ConfigPath,
		homeDir:    homeDir,
		ctx:        ctx,
		cancel:     cancel,
		sessions:   map[string]*Session{},
	}
	// Watch the project inbox for subagent completion pointers. A finishing
	// fire-and-forget subagent appends one pointer line to .shell3_project/inbox.jsonl
	// (see Session.report); the watcher tails the file and injects each pointer
	// into the live session(s) as a short notification. Bounded by ctx (cancelled
	// at Close). No-op when no store is configured.
	if rt.store != nil {
		// Capture the store in a local: this goroutine outlives a Reload, which
		// swaps rt.store, so reading the field here would race the swap. The
		// inbox path is derived from workDir (unchanged across reloads), so the
		// captured store watches the same file for the runtime's lifetime.
		store := rt.store
		go func() { _ = store.Watch(ctx, rt.injectPointer) }()
	}
	return rt, nil
}

// injectPointer surfaces one completion pointer (read from the project inbox) to
// the live host. It reconstructs a notify.Notification, renders it, then
// interjects the rendered pointer into every live session, waking idle ones.
// The inbox carries no payload — Path
// points at the subagent transcript and Summary is the preview — so detail stays
// in the run's own jsonl and the injected line is a short pointer.
func (rt *Runtime) injectPointer(p runs.Pointer) {
	n := notify.Notification{
		Kind:       p.Kind,
		ID:         p.RunID,
		Transcript: p.Path,
		Preview:    p.Summary,
		Exit:       p.Exit,
		TS:         p.TS,
	}
	rt.mu.Lock()
	live := make([]*Session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		live = append(live, s)
	}
	rt.mu.Unlock()
	for _, s := range live {
		s.injectNotification(rt, n)
	}
}

// Events returns the out-of-turn event bus. One receiver drives N sessions.
// Buffered; if the host is not draining, Wake events coalesce (drop on full —
// the host re-checks inboxes on its next turn anyway).
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

// Telegram returns the parsed shell3.telegram{} config (zero value if absent).
func (rt *Runtime) Telegram() TelegramConfig { return rt.telegram }

// Cron returns the parsed cron jobs from shell3.telegram (nil if absent).
func (rt *Runtime) Cron() []CronJob { return rt.cron }

// Store returns the shared canonical store (nil if unavailable).
func (rt *Runtime) Store() *runs.Store { return rt.store }

// ConfigPath returns the absolute path of the shell3.lua this runtime was built
// from. An empty or relative spec path is resolved exactly the way construction
// (and Reload) resolves it — ~/.shell3/shell3.lua — so the
// result is the actual file a reload reads. Useful for self-reconfiguration
// surfaces that need to show the agent/operator which file to edit.
func (rt *Runtime) ConfigPath() (string, error) {
	return agentsetup.ResolveConfigPath(rt.configPath, rt.homeDir)
}

// SessionMeta summarizes one stored past conversation.
type SessionMeta struct {
	ID        string `json:"id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	LastAt    string `json:"last_at"` // RFC3339 of newest message; falls back to start. Sort key for the dashboard.
	NumMsgs   int    `json:"num_msgs"`
	Preview   string `json:"preview"`
}

// PastSessions lists up to limit recent stored conversations (newest first).
// Returns nil if no store is configured. NumMsgs and Preview are derived from the
// run's messages.jsonl, since the file-native meta carries only lifecycle data.
func (rt *Runtime) PastSessions(limit int) ([]SessionMeta, error) {
	if rt.store == nil {
		return nil, nil
	}
	rows, err := rt.store.ListSessions(limit)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(rows))
	for _, m := range rows {
		e := SessionMeta{ID: m.ID, StartedAt: m.StartedAt.Format("2006-01-02 15:04")}
		if !m.EndedAt.IsZero() {
			e.EndedAt = m.EndedAt.Format("2006-01-02 15:04")
		}
		last := m.LastAt
		if last.IsZero() {
			last = m.StartedAt // no messages yet — sort by when it started
		}
		e.LastAt = last.UTC().Format(time.RFC3339)
		// Derive the message count + a preview (newest assistant/user text) from the
		// run's jsonl. Best-effort: a read error leaves NumMsgs 0 / Preview "".
		if msgs, err := rt.store.LoadMessages(m.ID); err == nil {
			e.NumMsgs = len(msgs)
			e.Preview = previewOf(msgs)
		}
		out = append(out, e)
	}
	return out, nil
}

// previewOf returns a short preview for a run-list card: the newest non-empty
// user-or-assistant message text, truncated. Empty when there is nothing to show.
func previewOf(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if (m.Role == llm.RoleUser || m.Role == llm.RoleAssistant) && strings.TrimSpace(m.Content) != "" {
			return truncatePreviewRunes(strings.TrimSpace(m.Content))
		}
	}
	return ""
}

// SessionTurns returns the stored turns of one past conversation as
// HistoryEntry values (Role/Content only). Reads the run's messages.jsonl.
func (rt *Runtime) SessionTurns(id string) ([]HistoryEntry, error) {
	if rt.store == nil {
		return nil, nil
	}
	msgs, err := rt.store.LoadMessages(id)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, HistoryEntry{Role: string(m.Role), Content: m.Content})
	}
	return out, nil
}

// SessionMessages returns the full-fidelity stored messages of one past
// conversation as HistoryEntry values — tool calls, tool results, and reasoning
// (thinking) included. Reads the run's messages.jsonl so the dashboard's
// conversation replay matches the live Chat view. Returns nil if no store is
// configured.
func (rt *Runtime) SessionMessages(id string) ([]HistoryEntry, error) {
	if rt.store == nil {
		return nil, nil
	}
	msgs, err := rt.store.LoadMessages(id)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToEntry(m))
	}
	return out, nil
}

// subagentIDRe constrains transcript ids to a safe charset (alphanumerics plus
// '-'/'_'), preventing any path traversal when building the audit-file path.
// Subagent ids are caller-chosen (the agent picks one per spawn), so the charset
// excludes '/' and '.'.
var subagentIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SubagentInfo is a read-only listing of one spawned subagent's transcript, for
// the dashboard. Subagents are backgrounded `shell3` runs that stream a
// transcript to .shell3_project/agents/<id>.jsonl; this is derived from those files on
// disk.
type SubagentInfo struct {
	ID         string `json:"id"`               // transcript filename stem
	Transcript string `json:"transcript"`       // path under .shell3_project/agents
	Agent      string `json:"agent,omitempty"`  // persona, from the start event
	Task       string `json:"task,omitempty"`   // the spawn prompt, from the start event
	Status     string `json:"status"`           // "running" until a terminal event is seen, else "finished"
	Result     string `json:"result,omitempty"` // last assistant message (final answer)
	Time       string `json:"time,omitempty"`   // start timestamp (RFC3339)
	LastAt     string `json:"last_at"`          // newest event timestamp (RFC3339); the dashboard's sort key
}

// peekTranscript reads a subagent transcript and extracts the summary fields the
// dashboard's run cards show: agent/task/time from the `start` line, status from
// whether a terminal (`end`/`session_end`) line is present, and result from the
// last assistant message. Failures degrade gracefully to a "running" stub.
func peekTranscript(path string) SubagentInfo {
	info := SubagentInfo{Status: "running"}
	data, err := os.ReadFile(path)
	if err != nil {
		return info
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev struct {
			Kind    string `json:"kind"`
			Input   string `json:"input"`
			Persona string `json:"persona"`
			TS      string `json:"ts"`
			Text    string `json:"text"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.TS != "" {
			info.LastAt = ev.TS // events are append-ordered, so the last wins
		}
		switch ev.Kind {
		case "start":
			info.Agent, info.Task, info.Time = ev.Persona, ev.Input, ev.TS
		case "assistant_message":
			if ev.Text != "" {
				info.Result = ev.Text
			}
		case "end", "session_end":
			info.Status = "finished"
		}
	}
	return info
}

// SubagentList scans .shell3_project/agents/*.jsonl under the runtime root and returns
// one entry per transcript (newest first by mod time). The dashboard reads
// completed/running subagent transcripts from disk. Returns nil when the
// directory is absent.
func (rt *Runtime) SubagentList() ([]SubagentInfo, error) {
	dir := paths.AgentsDir(rt.workDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type row struct {
		info SubagentInfo
		mod  int64
	}
	var rows []row
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		var mod int64
		if fi, err := e.Info(); err == nil {
			mod = fi.ModTime().UnixNano()
		}
		path := filepath.Join(dir, name)
		info := peekTranscript(path)
		info.ID, info.Transcript = id, path
		rows = append(rows, row{info: info, mod: mod})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mod > rows[j].mod })
	out := make([]SubagentInfo, len(rows))
	for i, r := range rows {
		out[i] = r.info
	}
	return out, nil
}

// TranscriptEvent is one lossless event from a subagent's audit log.
type TranscriptEvent struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Role   string `json:"role,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	CallID string `json:"call_id,omitempty"`
}

// SubagentTranscript reads a subagent's audit JSONL (.shell3_project/agents/<id>.jsonl
// under the runtime root) and returns its events. Returns nil if absent.
func (rt *Runtime) SubagentTranscript(id string) ([]TranscriptEvent, error) {
	if !subagentIDRe.MatchString(id) {
		return nil, fmt.Errorf("shell3: invalid subagent id %q", id)
	}
	path := paths.AgentTranscript(rt.workDir, id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []TranscriptEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev TranscriptEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip malformed lines rather than failing the whole read
		}
		out = append(out, ev)
	}
	return out, nil
}

func (rt *Runtime) root() string                 { return rt.workDir }
func (rt *Runtime) baseContext() context.Context { return rt.ctx }

func (rt *Runtime) emit(ev HostEvent) {
	select {
	case rt.events <- ev:
	default: // bus full: drop (Wake is a hint, not a queue)
	}
}

// Session returns the live session named opts.Name, creating it if necessary.
// A closed runtime returns an error. An empty name gets a unique generated
// name ("s<N>"). Requesting an existing live name returns that session unchanged.
func (rt *Runtime) Session(opts SessionOpts) (*Session, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil, fmt.Errorf("shell3: runtime is closed")
	}
	if opts.Name == "" {
		// Advance until we find a name not already taken by a live session.
		for {
			rt.nextName++
			opts.Name = fmt.Sprintf("s%d", rt.nextName)
			if _, taken := rt.sessions[opts.Name]; !taken {
				break
			}
		}
	}
	if s, ok := rt.sessions[opts.Name]; ok {
		return s, nil
	}
	cfg, err := rt.sessionConfig(opts)
	if err != nil {
		return nil, err
	}
	// Open the sink before constructing anything stateful: a failure here must
	// not leak a partially-initialised session or a store row.
	sink, sinkCleanup, err := chat.OpenSink(opts.OutPath)
	if err != nil {
		return nil, err
	}
	s := newSession(cfg, func() {}, opts) // shared parts are the runtime's to clean
	s.shellInteractive = opts.ShellInteractive
	s.asker = opts.Asker
	s.opts = opts
	s.runtime, s.name = rt, opts.Name
	s.sink, s.sinkCleanup = sink, sinkCleanup
	// Set the per-session host standing reminders now that runtime+name are set:
	// the Environment + Delegation context (each gated by the active agent's
	// toggle). No-op when both toggles are off.
	s.applyHostReminders(rt)
	s.writeStartLine("(session " + opts.Name + ")")
	rt.sessions[opts.Name] = s
	// Subagent completions arrive via the runtime's single inbox watcher (see
	// NewRuntime), not a per-session socket — nothing to launch here.
	return s, nil
}

// forget removes a closed session from the registry. Called by Session.Close.
func (rt *Runtime) forget(name string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.sessions, name)
}

// Close closes all live sessions, then the shared parts. Idempotent; a second
// call is a no-op and returns nil.
func (rt *Runtime) Close() error {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return nil
	}
	rt.closed = true
	open := make([]*Session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		open = append(open, s)
	}
	rt.sessions = map[string]*Session{}
	rt.mu.Unlock()

	var firstErr error
	for _, s := range open {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	rt.cleanup()
	// Cancel the runtime ctx so any in-flight cron dispatch subprocess is killed
	// and its goroutine unwinds. Do NOT close rt.events: a late emit from a
	// finishing dispatch must not panic.
	if rt.cancel != nil {
		rt.cancel()
	}
	// Join in-flight cron dispatch goroutines (exec → wait → deliver Notice) so
	// they finish before Close returns. rt.closed was set true under rt.mu above,
	// so trackSubagent can no longer Add (no Wait race).
	rt.wg.Wait()
	return firstErr
}
