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

// reloadState is a reload's swap inputs, split from BuildParts so applyReload
// — which owns the generation lifecycle — can be driven by tests with fake
// configs and observable closers. parts may be nil in tests.
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

// Reload re-reads the config and applies it to the running Runtime without
// restarting the process — the host-side entry for /reload and the reload tool.
//
//   - Validate first: a new Parts is built from the config dir, and on ANY
//     error it is discarded with the running config untouched.
//   - Front-end idle: the CALLER ensures its session has no turn in flight.
//     Reload holds rt.mu, so it serializes against Session() and Close().
//   - Background work does NOT block: running jobs and subagent children keep
//     the Parts they were built with, and the OLD generation's teardown is
//     parked until they drain. Nothing running closes it immediately.
//   - In place: idle root sessions keep their identity and history; only cfg
//     and handlers are rebuilt. Subagent children are left untouched.
//     Decorator-registered host tools ARE re-applied.
//
// NOTE: the kept s.sess closed over the OLD cfg.ContextWindow, so a changed
// context_window reaches an already-live session only at restart. Rebuilding
// s.sess would drop its in-memory history, so it is deliberately not done.
func (rt *Runtime) Reload() (ReloadResult, error) {
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
		agents:        newParts.AgentCount(),
		models:        newParts.ModelCount(),
	})
}

// applyReload swaps the runtime onto an already-built generation and
// re-derives the idle sessions in place. It owns the generation lifecycle,
// closing or parking the old teardown, but does not build config — Reload does
// that, and tests inject a fake st. On error it runs st.cleanup and changes
// nothing.
func (rt *Runtime) applyReload(st reloadState) (ReloadResult, error) {
	// Registered before rt.mu.Lock so it runs AFTER the deferred unlock: a
	// reload rebuilds every idle session's cfg, dropping the decorator's host
	// tools, and re-applying calls locked Runtime methods.
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

	// 1. Collect the sessions to re-derive. Subagent children are skipped:
	// they are a running job's context and keep their own generation.
	var idle []*Session
	for _, s := range rt.sessions {
		if s.opts.ParentID != "" {
			continue // subagent child: leave it on its original generation
		}
		// Enforced here rather than trusted to every caller: swapping s.cfg
		// under a live turn races its config reads and swaps the config under
		// an active hook. Fail before touching anything.
		if s.isBusy() {
			st.cleanup()
			return ReloadResult{}, fmt.Errorf("reload: session %q has a turn in flight", s.name)
		}
		idle = append(idle, s)
	}

	// 2. Swap shared state, then close OR park the OLD generation's teardown.
	oldCleanup := rt.cleanup // closes old proxies, MCP connections, store handle
	rt.sessionConfig = st.sessionConfig
	rt.cleanup = st.cleanup
	rt.store = st.store
	rt.cron = st.cron
	rt.telegram = st.telegram
	rt.parts = st.parts
	// A running job still holds the old generation's handles, so its teardown
	// waits until they drain. Nothing running closes now.
	if rt.jobs != nil && len(rt.jobs.runningJobIDs()) > 0 {
		rt.parkedClosers = append(rt.parkedClosers, oldCleanup)
	} else {
		oldCleanup()
	}

	// 3. Re-derive each idle session in place (keep history s.sess).
	var notes []string
	// The concurrency cap is armed at NewRuntime and not rebuilt — live jobs
	// hold slots on it — so surface a changed knob rather than ignoring it.
	if rt.jobs != nil {
		newMax := st.maxConcurrent
		if newMax <= 0 {
			newMax = defaultMaxConcurrent
		}
		if rt.jobs.max != newMax {
			notes = append(notes, "background.max_concurrent change takes effect on restart")
		}
	}
	for _, s := range idle {
		cfg, err := rt.sessionConfig(s.opts)
		if err != nil {
			notes = append(notes, fmt.Sprintf("session %q: re-derive failed: %v", s.name, err))
			continue
		}
		// Under s.mu: Snapshot() reads s.cfg from other goroutines, so an
		// unlocked assignment is a torn read. applyHostReminders only reads
		// s.cfg and calls the chat layer, which locks itself.
		s.mu.Lock()
		s.cfg = cfg
		// The chat session survives the swap — it IS the history — but its
		// store handle belongs to the generation that is closing.
		s.sess.SetStore(st.store)
		// Same reason: its event observer belongs to the OLD generation, whose
		// dispatcher oldCleanup closes. Without repointing, an event:
		// subscriber goes silent on the first reload and stays silent.
		s.sess.SetOnEvent(cfg.OnEvent)
		s.handlers = chat.NewHandlers()
		// rt.sessionConfig rebuilt the cfg, Environment toggle included, so
		// the standing reminders are replaced wholesale.
		s.applyHostReminders()
		s.mu.Unlock()
		redecorate = append(redecorate, s)
	}

	return ReloadResult{
		Agents: st.agents,
		Models: st.models,
		Jobs:   len(st.cron),
		Notes:  notes,
	}, nil
}
