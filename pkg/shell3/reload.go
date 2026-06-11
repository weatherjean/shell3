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
//   - Validate first: a new Parts is built from the file (BuildParts → luacfg
//     validation). On ANY error the new Parts is discarded and the running
//     config is left untouched — Reload returns the error and changes nothing.
//   - Idle only: the CALLER must ensure no live session has a turn in flight
//     (the host gates on Session.isBusy). Reload holds rt.mu so it serializes
//     against Session()/Close().
//   - Full rebuild: the old cleanup() runs (closing the old VM, proxies, store
//     handle) and every swappable Runtime field is replaced.
//   - In place: live sessions keep their identity and history (s.sess); only
//     s.cfg + s.handlers are rebuilt. Active agent + /set params are restored
//     best-effort. Host-registered tools are NOT restored here — the host
//     re-applies them after Reload returns (they are not engine state).
//
// NOTE: the kept s.sess was built with a ContextWindowFor closure over the OLD
// cfg.ContextWindow, so a changed context_window for an already-live session is
// not picked up until restart (new sessions get it). We deliberately do NOT
// rebuild s.sess — that would drop in-memory conversation history.
func (rt *Runtime) Reload() (ReloadResult, error) {
	// 1. Build + validate the new parts BEFORE touching anything.
	homeDir := rt.homeDir
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	newParts, newCleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: rt.configPath, CWD: rt.workDir, HomeDir: homeDir,
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

	// 2. Capture per-session overrides to restore after the swap.
	type override struct {
		s      *Session
		agent  string
		params map[string]string
	}
	var ovs []override
	for _, s := range rt.sessions {
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
	var cronJobs []CronJob
	for _, j := range newParts.Cron() {
		cronJobs = append(cronJobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
	tg := newParts.Telegram()
	rt.sessionConfig = func(o SessionOpts) (chat.Config, error) {
		return newParts.SessionConfig(agentsetup.SessionOptions{
			Agent: o.Agent, WorkDir: o.WorkDir,
			Headless: o.Headless, OutPath: o.OutPath,
		})
	}
	rt.subagentDesc = newParts.SubagentDescription
	rt.cleanup = newCleanup
	rt.store = newParts.Store()
	rt.cron = cronJobs
	rt.telegram = TelegramConfig{
		Token: tg.Token, ChatID: tg.ChatID, Agent: tg.Agent, WorkDir: tg.WorkDir,
		Dashboard: DashboardConfig{Enabled: tg.Dashboard.Enabled, Addr: tg.Dashboard.Addr, URL: tg.Dashboard.URL},
	}
	oldCleanup()

	// 4. Re-derive each live session in place (keep history s.sess), restore overrides.
	var notes []string
	for _, ov := range ovs {
		s := ov.s
		cfg, err := rt.sessionConfig(s.opts)
		if err != nil {
			notes = append(notes, fmt.Sprintf("session %q: re-derive failed: %v", s.name, err))
			continue
		}
		s.cfg = cfg
		s.handlers = chat.NewHandlers(cfg)
		// Re-apply the per-session Delegation context: rt.sessionConfig rebuilt the
		// prompt from the reloaded config without it. Idempotent (strips any prior
		// section first), so a following SwitchAgent re-applying it is harmless.
		s.applyDelegationContext(rt)
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
	}

	res := ReloadResult{
		Agents: len(newParts.AgentNames()),
		Models: newParts.ModelCount(),
		Jobs:   len(cronJobs),
		Notes:  notes,
	}
	return res, nil
}
