package chat

// Host-managed context compaction and pruning: the two-tier token-threshold
// system (prune_at stubs old tool outputs cheaply; compact_at summarizes the
// head while keeping recent turns verbatim), the manual /prune and forced
// /compact paths, and the token-estimate helpers they share. RunTurn calls
// maybeCompact at turn start; everything here is best-effort and never fails
// the user's turn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
)

// compactionFloor is the minimum head size (number of messages to be summarized,
// i.e. the cut index) required before auto-compaction will run. Below this there
// is too little history for a summary to free meaningful context, so compacting
// only adds an LLM round-trip and a boilerplate summary message for no benefit.
const compactionFloor = 8

// compactionFloorTokens is the alternative, token-based floor: a head with fewer
// than compactionFloor messages still compacts when its estimated tokens reach
// this, so a short-but-huge head (e.g. a couple of giant tool results) — exactly
// what needs collapsing — is not skipped by the message-count floor alone.
const compactionFloorTokens = 4096

// keepRecentFraction is the default fraction of compact_at preserved as the
// verbatim tail when keep_recent is unset.
const keepRecentFraction = 33 // percent

// minKeepRecent floors the verbatim tail (in estimated tokens) when keep_recent
// resolves to 0 — e.g. a forced /compact while auto-compaction is off
// (compact_at=0). Without it the tail would be empty and the entire
// conversation, including the latest turn, would be summarized away.
const minKeepRecent = 4096

// compactionTimeout bounds the synchronous summarisation round-trip so a
// stalled provider cannot freeze turn start indefinitely — the turn then
// proceeds on the un-compacted history like any other compaction failure.
const compactionTimeout = 2 * time.Minute

// resolveKeepRecent returns the tail size in prompt tokens: the explicit
// cfg.KeepRecent when set, otherwise a fraction of compact_at.
func resolveKeepRecent(cfg TurnConfig) int {
	if cfg.KeepRecent > 0 {
		return cfg.KeepRecent
	}
	return cfg.CompactAt * keepRecentFraction / 100
}

// compactionInstruction is the system prompt for the single quiet LLM call that
// produces the auto-compaction summary. It asks for a thorough narrative the
// continuation can resume from. Pointer lists are folded into the narrative
// here, so the auto path keeps CompactSummary's optional list fields empty.
const compactionInstruction = "You are compacting a long coding-assistant conversation to free context. " +
	"Write a thorough narrative summary of the conversation so far that a fresh continuation could resume from with no other context. " +
	"Cover: the user's goal and any decisions made; code written and files created or modified (with paths); commands run and their outcomes; errors encountered and how they were resolved; references worth keeping (session ids, commit hashes, URLs); and any confirmed open next steps. " +
	"Be comprehensive but do not invent detail. Output ONLY the summary prose — no preamble, no tool calls."

// maybeCompact is the turn-start context-management dispatcher. Two tiers keyed
// off the prior turn's real prompt-token count: at or above compact_at it
// summarises the head and keeps the tail (compactNow); in the band
// [prune_at, compact_at) it cheaply stubs old tool outputs (pruneOldToolOutputs).
// It is strictly best-effort: it must NEVER abort or fail the user's turn — on
// any problem it logs and proceeds on the un-compacted history (compactNow does
// make one synchronous summarisation round-trip, so it is not instantaneous).
//
// lastPromptTokens is 0 on the first turn, so the first turn never compacts or
// prunes.
func maybeCompact(ctx context.Context, cfg TurnConfig, sess *Session) {
	// A queued /compact forces a compaction regardless of the threshold (and even
	// when auto-compaction is disabled). Swap clears the request atomically.
	forced := sess.forceCompact.Swap(false)
	if !forced && cfg.CompactAt <= 0 {
		return
	}
	if forced || sess.lastPromptTokens >= cfg.CompactAt {
		compactNow(ctx, cfg, sess, forced)
		return
	}
	if cfg.PruneAt > 0 && sess.lastPromptTokens >= cfg.PruneAt {
		pruneOldToolOutputs(cfg, sess)
	}
}

// ErrNothingToCompact is returned by CompactStandalone when there is no head
// to summarise — the verbatim tail already covers the whole (short) history.
var ErrNothingToCompact = errors.New("nothing to compact")

// CompactStandalone runs one forced compaction outside a turn — the host-side
// /compact command. Returns estimated prompt tokens before/after (same
// estimator for both, so the delta is apples-to-apples). ErrNothingToCompact
// when history is too small to have a summarisable head; any other error means
// the summarisation call or the runs-session roll failed and history is
// untouched. Callers must hold the session's busy gate (see
// shell3.Session.Compact) — this mutates sess.messages like a turn would.
func CompactStandalone(ctx context.Context, cfg TurnConfig, sess *Session) (before, after int, err error) {
	return compactApply(ctx, cfg, sess, true)
}

// compactNow is the turn-start auto-compaction wrapper around compactApply:
// strictly best-effort, never fails the turn — failures were already logged
// inside compactApply, so the result is deliberately discarded.
func compactNow(ctx context.Context, cfg TurnConfig, sess *Session, forced bool) {
	_, _, _ = compactApply(ctx, cfg, sess, forced)
}

// compactApply performs host-enforced compaction: it summarises the head of
// the conversation and rebuilds history as that summary plus the verbatim recent
// tail. Reached from the auto path when the prompt token count hits compact_at,
// and from the forced paths (queued /compact, CompactStandalone). On any problem
// (too little history, an LLM error, an empty summary) it logs when warranted
// and returns an error WITHOUT compacting, so callers proceed on the
// un-compacted history. After a successful compaction lastPromptTokens is reset
// to the rewritten history's (small) estimated size so the threshold is not
// immediately re-tripped next turn. Returns the estimated prompt tokens before
// and after (equal when nothing was applied).
func compactApply(ctx context.Context, cfg TurnConfig, sess *Session, forced bool) (before, after int, err error) {
	before = estimatePromptTokens(sess.messages)
	// Compute the tail boundary before checking the floor: if the entire history
	// fits within keepRecent, there is nothing left to summarise.
	keepRecent := resolveKeepRecent(cfg)
	if keepRecent <= 0 {
		// A forced /compact can reach here with compact_at=0 (auto-compaction
		// off), which makes resolveKeepRecent return 0. Floor the tail so a forced
		// compaction never summarizes away the most recent turns.
		keepRecent = minKeepRecent
	}
	cut := compactionCut(sess.messages, keepRecent)
	if cut <= 0 || cut >= len(sess.messages) {
		// There is no head to summarise: the tail already covers everything
		// meaningful, or the snap-forward over a trailing all-tool run consumed the
		// whole tail (compacting here would summarize the latest turn away).
		return before, before, ErrNothingToCompact
	}
	head := sess.messages[:cut]
	// Floor check (auto path only — a forced /compact always proceeds when there
	// is a head). Skip only when the head is BOTH few messages AND few tokens: a
	// short head with many tokens (a couple of giant tool results) is exactly
	// what compaction should collapse, so the message-count floor alone would
	// wrongly no-op and leave context growing unbounded.
	if !forced && cut < compactionFloor && estimatePromptTokens(head) < compactionFloorTokens {
		return before, before, ErrNothingToCompact
	}
	tail := sess.messages[cut:]

	// One quiet LLM call: summarise only the head we are about to discard. We
	// accumulate text WITHOUT emitting any Token/assistant events — the user
	// should not see the summary stream as if it were a turn response.
	compactMsgs := make([]llm.Message, 0, len(head)+1)
	compactMsgs = append(compactMsgs, llm.Message{Role: llm.RoleSystem, Content: compactionInstruction})
	compactMsgs = append(compactMsgs, head...)

	cfg.Log.Debug("auto-compaction starting", "head_msgs", len(head), "forced", forced)
	cctx, cancel := context.WithTimeout(ctx, compactionTimeout)
	defer cancel()
	summary, serr := streamQuiet(cctx, cfg.LLM, compactMsgs)
	if serr != nil {
		cfg.Log.Warn("auto-compaction LLM call failed; proceeding on un-compacted history", "error", serr)
		return before, before, fmt.Errorf("compaction LLM call failed: %w", serr)
	}
	if strings.TrimSpace(summary) == "" {
		cfg.Log.Warn("auto-compaction produced an empty summary; proceeding on un-compacted history")
		return before, before, errors.New("compaction produced an empty summary")
	}

	// Build the file manifest from the head we are about to discard: files
	// modified with edit_file, deduplicated and capped.
	modified := extractFileManifest(head)
	summaryArgs := CompactSummary{Summary: summary, ImportantFiles: modified}

	// Rebuild history: continuation summary + verbatim tail. compactInto
	// rewrites sess.messages in place and rolls the store session.
	// RunTurn rebuilds its own allMsgs after maybeCompact returns.
	prevTokens := sess.lastPromptTokens
	// Same Meta the front-ends write on a fresh session: the rolled session
	// keeps the model recorded in its metadata.
	_, metaModel := SplitStatus(cfg.StatusLine)
	if !compactInto(summaryArgs, cfg.Store, sess, tail, cfg.Log, cfg.WorkDir, cfg.ConfigDir, metaModel) {
		// The runs-session roll failed; history is untouched. Proceed on the
		// un-compacted history without resetting the gauge or emitting a
		// (misleading) compacted event.
		return before, before, errors.New("runs-session roll failed; history untouched")
	}

	// Reset the token gauge to the rewritten history's (small) estimate so the
	// next turn does not immediately re-trip the threshold before a real usage
	// count from the provider lands.
	newTokens := estimatePromptTokens(sess.messages)
	sess.lastPromptTokens = newTokens
	// The context-usage reminder tracker remembers the last emitted bucket and
	// token mark across turns; without resetting it here, those stale (high)
	// values would suppress every context reminder as the conversation re-grows
	// from the post-compaction low back up through the same band.
	sess.reminders.resetContextGauge()

	emitCompacted(sess, prevTokens, newTokens)
	return before, newTokens, nil
}

// pruneOldToolOutputs stubs large tool results that sit before the protected
// recent tail, with no LLM call. It is the cheap first tier of context relief;
// only the manual /prune and full compaction persist — this mutates the
// in-memory slice only (the append-only JSONL keeps originals). Idempotent: a
// stub is far below pruneMinBytes, so re-running skips it.
func pruneOldToolOutputs(cfg TurnConfig, sess *Session) {
	cut := compactionCut(sess.messages, resolveKeepRecent(cfg))
	changed := false
	sess.msgMu.Lock()
	for i := 0; i < cut && i < len(sess.messages); i++ {
		m := &sess.messages[i]
		if m.Role == llm.RoleTool && len(m.Content) > pruneMinBytes {
			m.Content = pruneStub("pruned", len(m.Content))
			changed = true
		}
	}
	sess.msgMu.Unlock()
	if changed {
		sess.lastPromptTokens = estimatePromptTokens(sess.messages)
	}
}

// streamQuiet calls the LLM once and returns only the accumulated assistant
// text, emitting NO chat.Events. It is the non-emitting sibling of streamOnce,
// used by maybeCompact so the auto-compaction round-trip is invisible to the
// user/UI. Tool calls and reasoning are ignored; usage is discarded.
func streamQuiet(ctx context.Context, client LLMClient, msgs []llm.Message) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var sb strings.Builder
	err := client.Stream(ctx, msgs, nil, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
		}
	})
	if ctx.Err() != nil {
		return sb.String(), ctx.Err()
	}
	return sb.String(), err
}

// mediaPartTokens is the rough per-part estimate charged for a non-text
// content part (image or audio). Real provider costs vary with resolution /
// duration; this only needs to be the right order of magnitude so multimodal
// histories register on the prune/compaction thresholds at all.
const mediaPartTokens = 1000

// msgTokens approximates one message's token cost as (content + reasoning +
// tool-call argument bytes + content-part text bytes) / 4, plus a flat estimate
// per media part. Reasoning content is counted because the adapter re-sends it
// to the provider (see llm.Message.ReasoningContent), so it occupies real
// prompt tokens the tail-sizing walk must not under-count.
func msgTokens(m llm.Message) int {
	n := len(m.Content) + len(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		n += len(tc.RawArgs)
	}
	media := 0
	for _, p := range m.ContentParts {
		n += len(p.Text)
		if p.ImageURL != "" || p.AudioData != "" {
			media++
		}
	}
	return n/4 + media*mediaPartTokens
}

// estimatePromptTokens approximates the token count for a message slice. The
// slice reflects pruning in-place, so this automatically accounts for freed
// context.
func estimatePromptTokens(msgs []llm.Message) int {
	var total int
	for _, m := range msgs {
		total += msgTokens(m)
	}
	return total
}

// compactionCut returns the index in msgs at which the preserved tail begins:
// the most recent messages whose estimated tokens sum to at least keepRecent,
// snapped FORWARD past any leading tool message so the tail never begins with an
// orphan tool result (an OpenAI-compatible request rejects a tool message whose
// assistant tool_call is absent). The head is msgs[:cut]; the tail is msgs[cut:].
// Returns len(msgs) when keepRecent <= 0 (no tail kept).
func compactionCut(msgs []llm.Message, keepRecent int) int {
	if keepRecent <= 0 {
		return len(msgs)
	}
	total, cut := 0, len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		total += msgTokens(msgs[i])
		cut = i
		if total >= keepRecent {
			break
		}
	}
	for cut < len(msgs) && msgs[cut].Role == llm.RoleTool {
		cut++
	}
	return cut
}

// CompactSummary is the structured product of one compaction: a narrative
// summary plus the file-pointer lists the host-driven auto-compaction path
// (maybeCompact) derives from the compacted head's tool calls.
type CompactSummary struct {
	Summary        string
	ImportantFiles []string // files modified (edit_file) in the compacted head
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
func compactInto(args CompactSummary, st *runs.Store, sess *Session, tail []llm.Message, lg applog.Logger, workDir, configDir, model string) bool {
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
		newID, err := st.NewSession(runs.Meta{Workdir: workDir, ConfigDir: configDir, Model: model})
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
	fmt.Fprintf(&b, "<system-reminder>\nContinuation of session %s. History compacted.\nPrior session messages are in the runs directory (use the `history` skill, or read %s/runs/%s/messages.jsonl directly).\n</system-reminder>\n\n", prevSessionID, paths.ProjectDirName, prevSessionID)
	fmt.Fprintf(&b, "<compact-summary>\n%s\n</compact-summary>", args.Summary)
	writeBulletSection(&b, "modified-files", args.ImportantFiles)

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
		// Advance the high-water mark only past what actually reached disk; a
		// partial flush (e.g. full disk) leaves the rest for the next saveHistory
		// rather than skipping it.
		sess.persistedLen = flushMessages(st, lg, newSessionID, newMsgs)
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

// extractFileManifest scans the compacted head's structured tool calls for
// files modified with edit_file (edit_file.file_path). Malformed tool args are
// skipped. The list is capped at manifestCap, first-seen order preserved.
func extractFileManifest(head []llm.Message) (modified []string) {
	modSeen := map[string]bool{}
	for _, m := range head {
		if m.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name != "edit_file" {
				continue
			}
			if p := jsonPathArg(tc.RawArgs, "file_path"); p != "" && !modSeen[p] {
				modSeen[p] = true
				modified = append(modified, p)
			}
		}
	}
	return capStrings(modified, manifestCap)
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
// /prune slash command (internal/shell3.Session.Prune); element mutations propagate
// to the caller's slices.
func PruneByID(toolCallID, stem string, msgSlices ...[]llm.Message) (summary string, ok bool) {
	var target *llm.Message
	var name string
	for _, msgs := range msgSlices {
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
	for _, msgs := range msgSlices {
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
