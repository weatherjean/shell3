package shell3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/chat"
)

type subagent struct {
	id     string
	agent  string // registered subagent name (not the persona/agent name)
	task   string
	status string // "running" | "finished"
	result string
}

// subRegistry holds a session's spawned subagents. Guarded so list_agents (turn
// goroutine) and completion (subagent goroutine) don't race.
type subRegistry struct {
	mu   sync.Mutex
	subs []*subagent
}

// add registers a running entry; agentName is the registered subagent name.
// The id is supplied by the caller so the registry entry and the child's
// "sub:<id>" session name always agree.
func (r *subRegistry) add(id, agentName, task string) *subagent {
	r.mu.Lock()
	defer r.mu.Unlock()
	sa := &subagent{id: id, agent: agentName, task: task, status: "running"}
	r.subs = append(r.subs, sa)
	return sa
}

// remove drops sa from the registry. Used to undo an add when the goroutine
// that would have run the subagent could not be started (runtime closing).
func (r *subRegistry) remove(sa *subagent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.subs {
		if s == sa {
			r.subs = append(r.subs[:i], r.subs[i+1:]...)
			return
		}
	}
}

func (r *subRegistry) finish(sa *subagent, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sa.status = "finished"
	sa.result = result
}

func (r *subRegistry) snapshot() []chat.AgentSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]chat.AgentSnapshot, 0, len(r.subs))
	for _, sa := range r.subs {
		preview := sa.result
		if len(preview) > 200 {
			// Truncate on a rune boundary so we never split a multibyte UTF-8
			// rune (which would marshal to U+FFFD / invalid UTF-8).
			cut := 200
			for cut > 0 && !utf8.RuneStart(preview[cut]) {
				cut--
			}
			preview = preview[:cut] + "…"
		}
		out = append(out, chat.AgentSnapshot{ID: sa.id, Agent: sa.agent, Task: sa.task, Status: sa.status, Result: preview})
	}
	return out
}

func (s *Session) spawn(_ context.Context, req chat.SpawnRequest) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot spawn subagents")
	}
	if strings.TrimSpace(req.Subagent) == "" {
		return "", fmt.Errorf("shell3: spawn requires a subagent name")
	}
	workdir := req.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	// Mint a runtime-global id first, then create the child session. Only once
	// the child exists do we record the subagent in the registry — so a failed
	// MkdirAll/Session leaves no phantom forever-"running" entry.
	rt := s.runtime
	id := rt.nextSubID()
	auditPath := filepath.Join(rt.root(), ".shell3", "agents", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return "", err
	}
	child, err := rt.Session(SessionOpts{
		Name: "sub:" + id, Subagent: req.Subagent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err
	}
	sa := s.subs.add(id, req.Subagent, req.Task)
	// Fresh runtime-scoped context: the subagent must OUTLIVE the spawning turn
	// (its result arrives after the turn ends). Bounded by Runtime.Close.
	// Capture rt up front (above): Session.Close (incl. via Runtime.Close) nils
	// s.runtime, which would race with a read inside the goroutine.
	runCtx := rt.baseContext()
	started := rt.trackSubagent(func() {
		var b strings.Builder
		for ev := range child.Send(runCtx, req.Task) {
			if ev.Kind == Token { // assistant text → accumulate the result
				b.WriteString(ev.Text)
			}
		}
		result := strings.TrimSpace(b.String())
		s.subs.finish(sa, result)
		_ = child.Close()
		s.deliverSubagentResult(rt, sa.id, result)
	})
	if !started {
		// Runtime is closing: undo the registry entry and tear down the child we
		// just created, then report the spawn failure (nothing left dangling).
		s.subs.remove(sa)
		_ = child.Close()
		return "", fmt.Errorf("shell3: runtime is closing; cannot spawn subagents")
	}
	return id, nil
}

// deliverSubagentResult posts a finished subagent's result to the parent inbox,
// then Wakes the parent if it is idle (so the host runs a turn to react). rt is
// captured at spawn time so a concurrent parent Close (which nils s.runtime)
// can't race the Wake emit; the runtime struct itself outlives the goroutine.
func (s *Session) deliverSubagentResult(rt *Runtime, id, result string) {
	s.sess.Interject(fmt.Sprintf("subagent %s finished: %s", id, result))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}
