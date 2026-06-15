package shell3

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/paths"
)

// DispatchOpts parameterizes a host-initiated subagent dispatch (e.g. cron).
type DispatchOpts struct {
	WorkDir string // "" → main session workdir; relative → joined to it
	Label   string // tags the delivered result, e.g. "cron:nightly" → "[cron:nightly] …"
	Notify  bool   // deliver the result to the parent on success; failures always deliver
}

// Dispatch runs an agent from the host (not a model turn) and reports its result
// back into THIS (main) session's chat as a Notice (operator notification),
// waking nothing and touching no agent inbox. It is the cron/host-side trigger.
//
// Unlike a model-spawned subagent (a bash_bg-backgrounded `shell3` that
// self-reports an agent_done to the session and inject+wakes the agent),
// cron Dispatch must stay an OPERATOR notice: it execs a `shell3 run --config
// <cfg> --agent <agent> --out <transcript> "<prompt>"` SUBPROCESS, waits for it, reads
// the final assistant text from the transcript, and emits a chat Notice via
// deliverDispatchResult. It deliberately does NOT inject+wake the agent — that
// is wrong for a host-initiated job that must not start a hidden model turn or
// pollute the conversation.
//
// notify gating: on a successful run the Notice is delivered only when Notify is
// true; a run that ends in a terminal error ALWAYS delivers, so a quiet
// background job can never fail silently. The subprocess is tracked on the
// runtime waitgroup (trackSubagent) so Runtime.Close joins a still-running cron
// job before tearing the shared parts down; the runtime base context is the
// subprocess's parent, so Close's cancel also kills it. Returns the dispatch id
// (the transcript filename stem).
func (s *Session) Dispatch(agent, prompt string, opts DispatchOpts) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot dispatch")
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
	// Capture rt up front: Session.Close (incl. via Runtime.Close) nils s.runtime,
	// which would race a read inside the tracked goroutine below.
	rt := s.runtime
	id := rt.nextSubID()
	transcript := paths.AgentTranscript(rt.root(), id)
	if err := os.MkdirAll(paths.AgentsDir(rt.root()), 0o755); err != nil {
		return "", err
	}
	// The id scheme (a1, a2, …) resets each process, so this path can collide
	// with a leftover transcript from a prior run. Remove any stale file first:
	// if the child dies before writing, ReadTranscript must not surface old
	// content as this run's result (the child truncates it fresh when it starts).
	if err := os.Remove(transcript); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("shell3: dispatch: clear stale transcript: %w", err)
	}

	// Resolve the shell3 binary and the config file the child should reload. A
	// failure to resolve the config path is fatal to the dispatch (the child
	// could otherwise load a different config than this runtime).
	bin := shell3Binary()
	cfgPath, err := rt.ConfigPath()
	if err != nil {
		return "", fmt.Errorf("shell3: dispatch: resolve config path: %w", err)
	}

	label := opts.Label
	if label == "" {
		label = "dispatch"
	}
	notify := opts.Notify
	// The subprocess is parented by the runtime base context so Runtime.Close
	// (which cancels it) kills a long-running cron job; the goroutine waits on
	// the process, so Close's wg.Wait then joins cleanly.
	runCtx := rt.baseContext()

	// NOTE: the `run` subcommand is required — --out/--id and subagent-capable
	// --agent live there, not on the root command. Without it the child hits the
	// interactive root, which rejects --out ("unknown flag") and exits non-zero.
	args := []string{
		"run",
		"--config", cfgPath,
		"--agent", agent,
		"--out", transcript,
		prompt,
	}
	started := rt.trackSubagent(func() {
		cmd := exec.CommandContext(runCtx, bin, args...)
		cmd.Dir = workdir
		runErr := cmd.Run()
		// Read the final assistant text + error status from the transcript the
		// child streamed. A non-zero exit (runErr) OR an error recorded in the
		// transcript marks the run failed — either always notifies.
		tr := chat.ReadTranscript(transcript)
		failed := runErr != nil || tr.Errored
		result := tr.FinalText
		if result == "" && failed {
			// No assistant text to show, but the failure must still surface a
			// reason so a quiet job doesn't fail silently with an empty notice.
			if runErr != nil {
				result = "error: " + runErr.Error()
			} else {
				result = "error: run reported failure (see transcript " + transcript + ")"
			}
		}
		if notify || failed {
			s.deliverDispatchResult(rt, fmt.Sprintf("[%s] %s", label, result))
		}
	})
	if !started {
		return "", fmt.Errorf("shell3: runtime is closing; cannot dispatch")
	}
	return id, nil
}

// shell3Binary returns the path to the running shell3 executable so a dispatch
// (or a delegation command) re-execs the same binary. Falls back to the bare
// name "shell3" (resolved via PATH) when os.Executable is unavailable — e.g. a
// stripped/edge environment — which keeps dispatch working in the common case.
// It is a var so a test can substitute a stub binary for the cron subprocess.
var shell3Binary = func() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "shell3"
}

// deliverDispatchResult surfaces a finished host/cron dispatch result as a direct
// chat Notice on this session — shown verbatim, NOT injected into the agent's
// inbox. A host-initiated job (cron) is a notification to the operator, so it must
// not trigger a hidden model turn or pollute the conversation history — in
// contrast to agent_done delivery, which DOES inject + wake because there the
// agent itself asked for the subagent's result.
func (s *Session) deliverDispatchResult(rt *Runtime, labeled string) {
	rt.emit(HostEvent{Session: s.name, Kind: Notice, Text: labeled})
}
