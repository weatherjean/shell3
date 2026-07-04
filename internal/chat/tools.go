package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// ErrHostToolNotFound is returned by a HostTool dispatcher when it does not
// recognize the called tool name, signaling dispatchCustomTool to fall through
// to the command-template (ResolveCustomTool) path. Any OTHER error from a
// HostTool is a real failure and is surfaced as-is.
var ErrHostToolNotFound = errors.New("host tool: name not handled")

// toolResult is the typed outcome of one tool call: the text recorded as the
// tool message plus whether it represents a failure. Every dispatch path in
// executeToolCalls produces one, so error-ness is carried as data instead of
// being re-derived by sniffing prefixes off the output string.
type toolResult struct {
	output  string
	isError bool
}

func okResult(out string) toolResult  { return toolResult{output: out} }
func errResult(out string) toolResult { return toolResult{output: out, isError: true} }

// classifyHandlerOutput types a built-in handler's output string. Handlers
// report in-band failures to the model as "error: …" strings (so the text and
// the flag can never disagree); this is the single place that convention is
// interpreted. Hook, validation, and dispatcher failures never pass
// through here — they construct typed errResults directly.
func classifyHandlerOutput(out string) toolResult {
	if strings.HasPrefix(out, "error:") {
		return errResult(out)
	}
	return okResult(out)
}

// dispatchCustomTool resolves a custom-tool call to its bash command + env and
// runs it. Foreground tools block and return the command's output (a non-zero
// exit is surfaced as an error result, "exited N"). Background tools dispatch
// onto the in-process job runtime (StartBashBg, same as bash_bg) and return the
// job id; completion is delivered as a notice on a later turn. The
// resolved command is the trusted author template, so its text BYPASSES on_tool_call
// rewriting/denylisting (the tool call itself still fires the chain by name) — the
// model supplies only env values, never the command string.
func dispatchCustomTool(ctx context.Context, cfg TurnConfig, name, rawArgs string) toolResult {
	// Host-registered Go tools (pkg/shell3.RegisterHostTool) return a result
	// string directly, so they dispatch here without the resolve-and-exec path.
	if cfg.HostTool != nil {
		out, err := cfg.HostTool(ctx, name, rawArgs)
		switch {
		case err == nil:
			return classifyHandlerOutput(out)
		case !errors.Is(err, ErrHostToolNotFound):
			// A real host-tool failure — surface it.
			return errResult("error: " + err.Error())
		}
		// errors.Is(err, ErrHostToolNotFound): this dispatcher doesn't own the
		// name — fall through to the command-template path below.
	}
	if cfg.ResolveCustomTool == nil {
		return errResult(fmt.Sprintf("error: unknown tool %q", name))
	}
	rt, err := cfg.ResolveCustomTool(name, rawArgs)
	if err != nil {
		return errResult("error: " + err.Error())
	}
	if rt.Background {
		if cfg.StartBashBg == nil {
			return errResult("error: background tools are not available")
		}
		jobID, err := cfg.StartBashBg(rt.Command, cfg.WorkDir, []string{"bash", "-c", rt.Command}, rt.Env)
		if err != nil {
			return errResult("error: " + err.Error())
		}
		return okResult(fmt.Sprintf("started background tool %s\nYou'll get a completion notice on your next turn. Do not poll.", jobID))
	}
	timeout := time.Duration(DefaultBashTimeoutSeconds) * time.Second
	if rt.Timeout > 0 {
		t := rt.Timeout
		if t > MaxBashTimeoutSeconds {
			t = MaxBashTimeoutSeconds
		}
		timeout = time.Duration(t) * time.Second
	}
	out, code := runBashCapture(ctx, []string{"bash", "-c", rt.Command}, cfg.WorkDir, rt.Env, timeout)
	if code != 0 {
		return errResult(fmt.Sprintf("error: command exited %d\n%s", code, out))
	}
	return okResult(out)
}

// CompactSummary is the structured product of one compaction: a narrative
// summary plus the file-pointer lists the host-driven auto-compaction path
// (maybeCompact) derives from the compacted head's tool calls.
type CompactSummary struct {
	Summary        string
	ImportantFiles []string // files modified (edit_file) in the compacted head
	ReadFiles      []string // files read (read) but not modified in the compacted head
}

// writeBulletSection appends a "<tag>\n- item\n</tag>" block to b, or nothing
// when items is empty.
func writeBulletSection(b *strings.Builder, tag string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n\n<%s>\n", tag)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}
	fmt.Fprintf(b, "</%s>", tag)
}

// compactInto replaces the conversation history with a structured summary
// followed by the preserved verbatim tail. It ends the current runs session and
// starts a new one so the compact boundary is visible in history. Callers pass
// `tail` = the recent sub-slice of sess.messages to keep (see compactionCut).
// Callers are responsible for validating that args.Summary is non-empty.
//
// Returns true when the compaction was applied. It returns false WITHOUT
// touching the in-memory history when the runs-session roll fails (e.g. a full
// disk): rewriting memory to the short slice while the outgoing session's JSONL
// still holds the full history would let the next saveHistory duplicate the tail
// into it. Aborting keeps the on-disk history coherent; the caller proceeds on
// the un-compacted history (compaction is best-effort).
func compactInto(args CompactSummary, st *runs.Store, sess *Session, tail []llm.Message, lg applog.Logger, workDir, configPath string) bool {
	prevSessionID := sess.id
	// newSessionID stays prevSessionID unless the runs-session roll below
	// succeeds; it is published into sess.id atomically with sess.messages under
	// msgMu, so a concurrent ID() reader never sees a torn id/messages pairing.
	newSessionID := prevSessionID
	rolled := false

	// Roll the runs session so the compact boundary is visible in history. Start
	// the NEW session FIRST: only if that succeeds do we flush and end the
	// outgoing one. A failed NewSession then leaves the outgoing session intact
	// (not ended, still persistable) and we abort the compaction below, rather
	// than ending a session we keep writing to and corrupting its JSONL.
	if st != nil {
		newID, err := st.NewSession(runs.Meta{Workdir: workDir, ConfigPath: configPath})
		if err != nil {
			lg.Warn("start session failed during compact; skipping compaction", "error", err)
			return false
		}
		// Flush only the unsaved tail of the outgoing session. Messages
		// 0..persistedLen-1 were already written by prior saveHistory calls;
		// re-flushing the full slice would duplicate those lines in the
		// append-only JSONL file. A guard mirrors saveHistory's own guard.
		if sess.persistedLen <= len(sess.messages) {
			flushMessages(st, lg, prevSessionID, sess.messages[sess.persistedLen:])
		}
		if err := st.EndSession(prevSessionID); err != nil {
			lg.Warn("end session failed during compact", "session_id", prevSessionID, "error", err)
		}
		newSessionID = newID
		rolled = true
	}

	// Build the continuation message injected at the top of the new history.
	var b strings.Builder
	fmt.Fprintf(&b, "<system-reminder>\nContinuation of session %s. History compacted.\nPrior session messages are in the runs directory (use the `history` skill, or read .shell3_project/runs/%s/messages.jsonl directly).\n</system-reminder>\n\n", prevSessionID, prevSessionID)
	fmt.Fprintf(&b, "<compact-summary>\n%s\n</compact-summary>", args.Summary)
	writeBulletSection(&b, "modified-files", args.ImportantFiles)
	writeBulletSection(&b, "read-files", args.ReadFiles)

	continuationMsg := llm.Message{Role: llm.RoleUser, Content: b.String()}

	// Build the rewritten history in a local, then publish it under msgMu: this
	// runs on the turn goroutine but replaces the slice the dashboard's
	// Messages() reader may be copying concurrently (see Session.msgMu).
	newMsgs := make([]llm.Message, 0, 1+len(tail))
	newMsgs = append(newMsgs, continuationMsg)
	newMsgs = append(newMsgs, tail...)
	sess.msgMu.Lock()
	sess.id = newSessionID
	sess.messages = newMsgs
	// Reminder anchors index the pre-compaction message slice; the rewrite
	// invalidates them. Drop the log exactly as SetMessages does — stale high-Seq
	// anchors otherwise break History()'s non-decreasing-Seq interleave and
	// silently hide every later reminder from the dashboard. The new runs session
	// has its own (empty) reminder sidecar, so no truncation is needed.
	sess.reminderLog = nil
	sess.msgMu.Unlock()

	// Mirror the compacted context into the runs store under the NEW session id,
	// so a resume of this session loads the within-window compacted history
	// rather than the pre-compaction blob. flushMessages above wrote the OUTGOING
	// session; this writes the incoming one.
	if rolled {
		flushMessages(st, lg, newSessionID, newMsgs)
		// The new session's messages are now persisted; advance the high-water
		// mark so the next saveHistory doesn't re-flush them.
		sess.persistedLen = len(newMsgs)
	} else {
		// No store configured: nothing was persisted, so start the high-water
		// mark fresh.
		sess.persistedLen = 0
	}
	return true
}

// manifestCap is the maximum number of file paths reported per list in the
// compaction manifest. Capping avoids bloating the continuation message when
// many files were touched in a long head.
const manifestCap = 20

// extractFileManifest scans the compacted head's structured tool calls for file
// paths: edit_file.file_path -> modified, read.path -> read. A file both read
// and modified appears only under modified. Malformed tool args are skipped.
// Each list is capped at manifestCap, first-seen order preserved.
func extractFileManifest(head []llm.Message) (modified, read []string) {
	modSeen, readSeen := map[string]bool{}, map[string]bool{}
	for _, m := range head {
		if m.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			switch tc.Name {
			case "edit_file":
				if p := jsonPathArg(tc.RawArgs, "file_path"); p != "" && !modSeen[p] {
					modSeen[p] = true
					modified = append(modified, p)
				}
			case "read":
				if p := jsonPathArg(tc.RawArgs, "path"); p != "" && !readSeen[p] {
					readSeen[p] = true
					read = append(read, p)
				}
			}
		}
	}
	// read-only = read minus modified.
	filtered := read[:0]
	for _, p := range read {
		if !modSeen[p] {
			filtered = append(filtered, p)
		}
	}
	return capStrings(modified, manifestCap), capStrings(filtered, manifestCap)
}

// jsonPathArg returns the named string field from a tool call's raw JSON args,
// or "" if absent/malformed.
func jsonPathArg(rawArgs, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// capStrings returns s[:n] when len(s) > n, otherwise s unchanged.
func capStrings(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// pruneMinBytes is the minimum content length (in bytes) for a tool result to
// be eligible for auto-pruning. Stubs are ~30 bytes, well below this threshold,
// making pruneOldToolOutputs naturally idempotent without a separate flag.
const pruneMinBytes = 2048

// pruneStub is the placeholder a pruned tool result is replaced with. Shared by
// the manual /prune command (PruneByID) and the automatic prune pass.
func pruneStub(stem string, origLen int) string {
	return fmt.Sprintf("[%s — original was %d bytes]", stem, origLen)
}

// PruneByID replaces the tool result with the given id in any of the slices
// with a short stem stub. summary is a human-readable status string; ok is
// false when no tool result with that id exists in the slices, so callers
// branch on the flag instead of parsing the summary. Used by the host-side
// /prune slash command (pkg/shell3.Session.Prune); element mutations propagate
// to the caller's slices.
func PruneByID(toolCallID, stem string, slices ...[]llm.Message) (summary string, ok bool) {
	var target *llm.Message
	var name string
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				target = &msgs[i]
				name = msgs[i].Name
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("error: no tool result with id %q in conversation", toolCallID), false
	}

	content := target.Content
	stub := pruneStub(stem, len(content))
	count := 0
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				msgs[i].Content = stub
				count++
			}
		}
	}
	if count == 0 {
		return "error: failed to update message content", false
	}
	return fmt.Sprintf("Pruned result of %s (id=%s): freed %d bytes", name, toolCallID, len(content)-len(stub)), true
}
