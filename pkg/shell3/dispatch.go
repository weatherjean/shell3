package shell3

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DispatchOpts parameterizes a host-initiated subagent dispatch (e.g. cron).
type DispatchOpts struct {
	WorkDir string // "" → main session workdir; relative → joined to it
	Label   string // tags the delivered result, e.g. "cron:nightly" → "[cron:nightly] …"
	Notify  bool   // deliver the result to the parent on success; failures always deliver
}

// Dispatch runs a registered subagent from the host (not a model turn) and
// reports its result back into THIS (main) session's inbox, waking it. It is the
// cron/host-side trigger for the same path the model's spawn_agent tool uses,
// inheriting unique ids, depth-1, Close-joins-goroutines, and result-to-inbox.
//
// notify gating: on a successful run the result is delivered (and the session
// woken) only when Notify is true; otherwise it is recorded in the subagent
// transcript only. A run that ends in a terminal error ALWAYS delivers, so a
// quiet background job can never fail silently. The terminal Error event is the
// trigger — intermediate Retry events are ignored, so we go loud once on the
// final failure, not per retry. Returns the subagent id.
func (s *Session) Dispatch(agent, prompt string, opts DispatchOpts) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot dispatch")
	}
	if strings.HasPrefix(s.name, "sub:") {
		return "", fmt.Errorf("shell3: dispatch is not allowed from a subagent session (depth-1)")
	}
	if strings.TrimSpace(agent) == "" {
		return "", fmt.Errorf("shell3: dispatch requires an agent name")
	}
	workdir := opts.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	rt := s.runtime
	id := rt.nextSubID()
	auditPath := filepath.Join(rt.root(), ".shell3", "agents", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return "", err
	}
	child, err := rt.Session(SessionOpts{
		Name: "sub:" + id, Subagent: agent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err
	}
	sa := s.subs.add(id, agent, prompt)
	label := opts.Label
	if label == "" {
		label = "dispatch"
	}
	notify := opts.Notify
	runCtx := rt.baseContext()
	started := rt.trackSubagent(func() {
		var b strings.Builder
		failed := false
		for ev := range child.Send(runCtx, prompt) {
			switch ev.Kind {
			case Token:
				b.WriteString(ev.Text)
			case Error:
				failed = true // terminal; Retry is a separate Kind and is ignored
				if ev.Err != nil {
					b.WriteString("\nerror: " + ev.Err.Error())
				}
			}
		}
		result := strings.TrimSpace(b.String())
		s.subs.finish(sa, result)
		_ = child.Close()
		if notify || failed {
			s.deliverDispatchResult(rt, fmt.Sprintf("[%s] %s", label, result))
		}
	})
	if !started {
		s.subs.remove(sa)
		_ = child.Close()
		return "", fmt.Errorf("shell3: runtime is closing; cannot dispatch")
	}
	return id, nil
}

// deliverDispatchResult surfaces a finished host/cron dispatch result as a direct
// chat Notice on this session — shown verbatim, NOT injected into the agent's
// inbox. A host-initiated job (cron) is a notification to the operator, so it must
// not trigger a hidden model turn or pollute the conversation history. This is the
// deliberate contrast with deliverSubagentResult, which DOES inject + wake because
// there the agent itself asked for the subagent's result mid-turn.
func (s *Session) deliverDispatchResult(rt *Runtime, labeled string) {
	rt.emit(HostEvent{Session: s.name, Kind: Notice, Text: labeled})
}
