package shell3

import (
	"fmt"
	"os"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// ReloadResult summarizes a successful reload, for the host's reply + log.
type ReloadResult struct {
	Agents int      // number of agents now live
	Models int      // number of models now live
	Jobs   int      // number of cron jobs now armed
	Notes  []string // human-readable notes
}

// Reload re-reads the config file the Runtime was built from and applies it to
// the running Runtime WITHOUT restarting the process. It is the host-side entry
// for self-reconfiguration (the /reload command and the agent reload tool).
//
// Contract:
//   - Validate first: a new Parts is built from the config dir (BuildParts → config
//     validation). On ANY error the new Parts is discarded and the running
//     config is left untouched — Reload returns the error and changes nothing.
//   - Idle only: the CALLER must ensure no live session has a turn in flight
//     (the host gates on Session.isBusy). Reload holds rt.mu so it serializes
//     against Session()/Close().
//   - Full rebuild: the old cleanup() runs (closing the old VM, proxies, store
//     handle) and every swappable Runtime field is replaced.
//   - In place: live sessions keep their identity and history (s.sess); only
//     s.cfg + s.handlers are rebuilt. Active agent + /set params are restored
//     best-effort. Decorator-registered host tools (SetSessionDecorator, e.g.
//     image_generate) ARE re-applied here; tools a host registered directly
//     (the bot's send/reload/status) are not — the host re-applies those
//     after Reload returns (they are not engine state).
//
// NOTE: the kept s.sess was built with a ContextWindowFor closure over the OLD
// cfg.ContextWindow, so a changed context_window for an already-live session is
// not picked up until restart (new sessions get it). We deliberately do NOT
// rebuild s.sess — that would drop in-memory conversation history.
func (rt *Runtime) Reload() (ReloadResult, error) {
	// 0. Refuse while background work is in flight: a running subagent's turn
	// would race the config swap, and a lingering child's follow-up turn could
	// start mid-rebuild. The user /stops (kills the turn + all jobs) or lets
	// them finish, then reloads. Checked before the config build so the
	// rejection is instant and side-effect free.
	if err := rt.jobs.errIfRunning("/reload"); err != nil {
		return ReloadResult{}, fmt.Errorf("reload: %w", err)
	}
	// Registered before rt.mu.Lock so it runs AFTER the deferred unlock (LIFO):
	// a successful reload rebuilt every live session's cfg, dropping decorator-
	// registered host tools (image_generate); re-apply the decorator outside
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
	// 1. Build + validate the new parts BEFORE touching anything.
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

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		newCleanup()
		return ReloadResult{}, fmt.Errorf("reload: runtime is closed")
	}
	// Re-check under rt.mu: a job may have started while the new parts were
	// building. Discard the built parts and change nothing.
	if err := rt.jobs.errIfRunning("/reload"); err != nil {
		newCleanup()
		return ReloadResult{}, fmt.Errorf("reload: %w", err)
	}

	// 2. Capture per-session overrides to restore after the swap.
	type override struct {
		s      *Session
		agent  string
		params map[string]string
	}
	var ovs []override
	for _, s := range rt.sessions {
		// Enforce the idle-only contract here rather than trusting every caller:
		// swapping s.cfg under a live turn would race the turn's config reads and
		// swap the config under an active hook. Fail before touching anything.
		if s.isBusy() {
			newCleanup()
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

	// 3. Swap shared state: close the OLD parts, install the new.
	oldCleanup := rt.cleanup // closes old VM, proxies, old store handle
	cronJobs := newParts.Cron()
	rt.sessionConfig = sessionConfigFrom(newParts)
	rt.cleanup = newCleanup
	rt.store = newParts.Store()
	rt.cron = cronJobs
	rt.telegram = newParts.Telegram()
	rt.web = newParts.Web()
	rt.heartbeat = newParts.Heartbeat()
	rt.parts = newParts
	oldCleanup()

	// 4. Re-derive each live session in place (keep history s.sess), restore overrides.
	var notes []string
	// The job manager's concurrency cap is armed at NewRuntime and not rebuilt
	// here (live jobs hold slots on it); surface a changed knob instead of
	// silently ignoring it.
	if newMax := newParts.BackgroundMaxConcurrent(); rt.jobs != nil {
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
		// Swap under s.mu: Snapshot() (dashboard/status tool) reads s.cfg under
		// s.mu from other goroutines, so an unlocked assignment is a torn-read
		// race. applyHostReminders only reads s.cfg + calls the chat layer (its
		// own locking) — safe to run inside the critical section.
		s.mu.Lock()
		s.cfg = cfg
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

	res := ReloadResult{
		Agents: len(newParts.AgentNames()),
		Models: newParts.ModelCount(),
		Jobs:   len(cronJobs),
		Notes:  notes,
	}
	return res, nil
}
