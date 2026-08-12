package shell3

import (
	"fmt"
	"os"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

// ReloadResult summarizes a successful reload, for the host's reply + log.
type ReloadResult struct {
	Agents int      // number of agents now live
	Models int      // number of models now live
	Jobs   int      // number of cron jobs now armed
	Notes  []string // human-readable notes
}

// reloadState is the applied side of a reload: the swap inputs, split out from
// BuildParts so applyReload (which holds the whole generation lifecycle) can be
// driven by tests with fake per-session configs and observable closers instead
// of a real config directory. parts may be nil in unit tests (rt.Parts() and
// its callers already tolerate a nil Parts).
type reloadState struct {
	sessionConfig func(SessionOpts) (chat.Config, error)
	cleanup       func()            // the new generation's teardown (old runs/parks)
	parts         *agentsetup.Parts // nil in unit tests
	store         *runs.Store       // new generation's runs store
	cron          []CronJob         // new cron jobs (armed by the host)
	telegram      TelegramConfig    // new telegram mirror
	maxConcurrent int               // background.max_concurrent (0 = default)
	agents        int               // agent count for the result
	models        int               // model count for the result
}

// Reload re-reads the config file the Runtime was built from and applies it to
// the running Runtime WITHOUT restarting the process. It is the host-side entry
// for self-reconfiguration (the /reload command and the agent reload tool).
//
// Contract:
//   - Validate first: a new Parts is built from the config dir (BuildParts → config
//     validation). On ANY error the new Parts is discarded and the running
//     config is left untouched — Reload returns the error and changes nothing.
//   - Front-end idle: the CALLER must ensure the front-end session it drives has
//     no turn in flight (the host gates on Session.isBusy). Reload holds rt.mu so
//     it serializes against Session()/Close().
//   - Background work does NOT block a reload: running bash_bg jobs and subagent
//     children keep the Parts they were built with (their store/MCP handles); the
//     OLD generation's teardown is deferred (parked) and runs once every such job
//     drains (drainParkedClosers). A reload with nothing running closes the old
//     generation immediately, as before.
//   - In place: idle front-end (root) sessions keep their identity and history
//     (s.sess); only s.cfg + s.handlers are rebuilt onto the new config. Subagent
//     child sessions (ParentID set) are left untouched — they keep the old
//     generation until they finish. Active agent + /set params are restored
//     best-effort. Decorator-registered host tools (SetSessionDecorator, e.g.
//     send_media_telegram) ARE re-applied here.
//
// NOTE: the kept s.sess was built with a ContextWindowFor closure over the OLD
// cfg.ContextWindow, so a changed context_window for an already-live session is
// not picked up until restart (new sessions get it). We deliberately do NOT
// rebuild s.sess — that would drop in-memory conversation history.
func (rt *Runtime) Reload() (ReloadResult, error) {
	// Build + validate the new parts BEFORE touching anything.
	homeDir := rt.homeDir
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	newParts, newCleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: rt.configDir, CWD: rt.workDir, HomeDir: homeDir,
	})
	if err != nil {
		if newCleanup != nil {
			newCleanup() // release anything the partial build opened
		}
		return ReloadResult{}, fmt.Errorf("reload: %w", err)
	}
	return rt.applyReload(reloadState{
		sessionConfig: sessionConfigFrom(newParts),
		cleanup:       newCleanup,
		parts:         newParts,
		store:         newParts.Store(),
		cron:          newParts.Cron(),
		telegram:      newParts.Telegram(),
		maxConcurrent: newParts.BackgroundMaxConcurrent(),
		agents:        len(newParts.AgentNames()),
		models:        newParts.ModelCount(),
	})
}

// applyReload swaps the runtime onto an already-built generation (st) and
// re-derives the idle front-end sessions in place. It owns the generation
// lifecycle — closing or parking the old teardown — but does NOT build config;
// Reload does that and tests inject a fake st directly. On the runtime-closed or
// a busy-front-end error it runs st.cleanup and changes nothing.
func (rt *Runtime) applyReload(st reloadState) (ReloadResult, error) {
	// Registered before rt.mu.Lock so it runs AFTER the deferred unlock (LIFO):
	// a successful reload rebuilt every idle session's cfg, dropping decorator-
	// registered host tools (send_media_telegram); re-apply the decorator outside
	// rt.mu (it calls locked Runtime methods such as Parts()).
	var redecorate []*Session
	defer func() {
		dec := rt.decorateFn()
		if dec == nil {
			return
		}
		for _, s := range redecorate {
			dec(s)
		}
	}()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		st.cleanup()
		return ReloadResult{}, fmt.Errorf("reload: runtime is closed")
	}

	// 1. Capture per-session overrides to restore after the swap. Subagent child
	// sessions are skipped: they are an already-running job's context and keep
	// the Parts they were built with (the old generation lingers for them).
	type override struct {
		s      *Session
		agent  string
		params map[string]string
	}
	var ovs []override
	for _, s := range rt.sessions {
		if s.opts.ParentID != "" {
			continue // subagent child: leave it on its original generation
		}
		// Enforce the front-end idle contract here rather than trusting every
		// caller: swapping s.cfg under a live turn would race the turn's config
		// reads and swap the config under an active hook. Fail before touching
		// anything (background jobs no longer block — only a front-end turn does).
		if s.isBusy() {
			st.cleanup()
			return ReloadResult{}, fmt.Errorf("reload: session %q has a turn in flight", s.name)
		}
		ov := override{s: s, agent: s.ActiveAgent(), params: map[string]string{}}
		for _, p := range s.Snapshot().Params {
			if p.Value != "" { // only explicit /set overrides
				ov.params[p.Name] = p.Value
			}
		}
		ovs = append(ovs, ov)
	}

	// 2. Swap shared state, then close OR park the OLD generation's teardown.
	oldCleanup := rt.cleanup // closes old proxies, MCP connections, store handle
	rt.sessionConfig = st.sessionConfig
	rt.cleanup = st.cleanup
	rt.store = st.store
	rt.cron = st.cron
	rt.telegram = st.telegram
	rt.parts = st.parts
	// A running job (or lingering subagent child) still holds the old
	// generation's store/MCP handles — defer its teardown until they drain
	// (drainParkedClosers, from job completion). Nothing running → close now.
	if rt.jobs != nil && len(rt.jobs.runningJobIDs()) > 0 {
		rt.parkedClosers = append(rt.parkedClosers, oldCleanup)
	} else {
		oldCleanup()
	}

	// 3. Re-derive each idle session in place (keep history s.sess), restore overrides.
	var notes []string
	// The job manager's concurrency cap is armed at NewRuntime and not rebuilt
	// here (live jobs hold slots on it); surface a changed knob instead of
	// silently ignoring it.
	if rt.jobs != nil {
		newMax := st.maxConcurrent
		if newMax <= 0 {
			newMax = defaultMaxConcurrent
		}
		if rt.jobs.max != newMax {
			notes = append(notes, "background.max_concurrent change takes effect on restart")
		}
	}
	for _, ov := range ovs {
		s := ov.s
		cfg, err := rt.sessionConfig(s.opts)
		if err != nil {
			notes = append(notes, fmt.Sprintf("session %q: re-derive failed: %v", s.name, err))
			continue
		}
		// Swap under s.mu: Snapshot() (the Status view) reads s.cfg under
		// s.mu from other goroutines, so an unlocked assignment is a torn-read
		// race. applyHostReminders only reads s.cfg + calls the chat layer (its
		// own locking) — safe to run inside the critical section.
		s.mu.Lock()
		s.cfg = cfg
		// The chat session survives the swap (it IS the history) but its sidecar
		// store handle is the old generation's, which closes when the parked
		// cleanup drains — repoint reminders at the new generation's store.
		s.sess.SetStore(st.store)
		s.handlers = chat.NewHandlers()
		// Re-apply the per-session host standing reminders: rt.sessionConfig
		// rebuilt the cfg (including the Environment toggle) from the
		// reloaded config. SetStandingReminders replaces the set wholesale, so a
		// following SwitchAgent re-applying it is harmless.
		s.applyHostReminders()
		s.mu.Unlock()
		// Restore active agent if it still exists, else fall back + note it.
		if ov.agent != "" && ov.agent != s.ActiveAgent() {
			if err := s.SwitchAgent(ov.agent); err != nil {
				notes = append(notes, fmt.Sprintf("agent %q no longer exists; using %q", ov.agent, s.ActiveAgent()))
			}
		}
		// Replay /set params best-effort.
		for name, val := range ov.params {
			_ = s.SetParam(name, val) // silently skip params the new model lacks
		}
		redecorate = append(redecorate, s)
	}

	return ReloadResult{
		Agents: st.agents,
		Models: st.models,
		Jobs:   len(st.cron),
		Notes:  notes,
	}, nil
}
