package shell3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/chat"
)

type subagent struct {
	id     string
	agent  string
	task   string
	status string // "running" | "finished"
	result string
}

// subRegistry holds a session's spawned subagents. Guarded so list_agents (turn
// goroutine) and completion (subagent goroutine) don't race.
type subRegistry struct {
	mu   sync.Mutex
	subs []*subagent
	seq  int
}

func (r *subRegistry) add(agent, task string) *subagent {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	sa := &subagent{id: fmt.Sprintf("a%d", r.seq), agent: agent, task: task, status: "running"}
	r.subs = append(r.subs, sa)
	return sa
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
			preview = preview[:200] + "…"
		}
		out = append(out, chat.AgentSnapshot{ID: sa.id, Agent: sa.agent, Task: sa.task, Status: sa.status, Result: preview})
	}
	return out
}

func (s *Session) spawn(_ context.Context, req chat.SpawnRequest) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot spawn subagents")
	}
	agent := req.Agent
	if agent == "" {
		agent = s.cfg.Personality.Name
	}
	workdir := req.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	sa := s.subs.add(agent, req.Task)
	auditPath := filepath.Join(s.runtime.root(), ".shell3", "agents", sa.id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return "", err
	}
	child, err := s.runtime.Session(SessionOpts{
		Name: "sub:" + sa.id, Agent: agent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err
	}
	// Fresh runtime-scoped context: the subagent must OUTLIVE the spawning turn
	// (its result arrives after the turn ends). Bounded by Runtime.Close.
	// Capture runCtx up front: Session.Close (incl. via Runtime.Close) nils
	// s.runtime, which would race with a read inside the goroutine.
	rt := s.runtime
	runCtx := rt.baseContext()
	go func() {
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
	}()
	return sa.id, nil
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
