package chat

// Host-managed context relief in two tiers: prune_at stubs old tool outputs
// cheaply, compact_at summarises the head and keeps recent turns verbatim.
// RunTurn calls maybeCompact at turn start; everything here is best-effort
// and never fails the user's turn.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// compactionFloor is the smallest head (in messages) auto-compaction will
// summarise. Below it a summary frees nothing and costs a round-trip.
const compactionFloor = 8

// compactionFloorTokens is the token-based alternative, so a short-but-huge
// head (two giant tool results) is not skipped by the message-count floor.
const compactionFloorTokens = 4096

// keepRecentFraction is the default fraction of compact_at preserved as the
// verbatim tail when keep_recent is unset.
const keepRecentFraction = 33 // percent

// minimumKeepRecent floors the verbatim tail when an unusually small
// compact_at makes the default fraction round to zero.
const minimumKeepRecent = 4096

// compactionTimeout bounds the summarisation round-trip so a stalled provider
// cannot freeze turn start; the turn then proceeds un-compacted.
const compactionTimeout = 2 * time.Minute

// resolveKeepRecent returns the tail size in prompt tokens: the explicit
// cfg.KeepRecent when set, otherwise a fraction of compact_at.
func resolveKeepRecent(cfg TurnConfig) int {
	if cfg.KeepRecent > 0 {
		return cfg.KeepRecent
	}
	return cfg.CompactAt * keepRecentFraction / 100
}

// compactionInstruction drives the one quiet LLM call that produces the
// summary. Pointer lists fold into the narrative, so the auto path leaves
// CompactSummary's list fields empty.
const compactionInstruction = "You are compacting a long coding-assistant conversation to free context. " +
	"Write a thorough narrative summary of the conversation so far that a fresh continuation could resume from with no other context. " +
	"Cover: the user's goal and any decisions made; code written and files created or modified (with paths); commands run and their outcomes; errors encountered and how they were resolved; references worth keeping (session ids, commit hashes, URLs); and any confirmed open next steps. " +
	"Be comprehensive but do not invent detail. Output ONLY the summary prose — no preamble, no tool calls."

// maybeCompact dispatches at turn start on the PRIOR turn's real prompt-token
// count: at or above compact_at it summarises the head (one synchronous
// round-trip, so not instantaneous); in [prune_at, compact_at) it stubs old
// tool outputs. It must NEVER fail the user's turn — on any problem it logs
// and proceeds un-compacted. lastPromptTokens is 0 on the first turn, so that
// turn never compacts.
func maybeCompact(ctx context.Context, cfg TurnConfig, sess *Session) {
	if cfg.CompactAt <= 0 {
		return
	}
	if sess.lastPromptTokens >= cfg.CompactAt {
		warnFixedOverhead(cfg, sess)
		compactNow(ctx, cfg, sess)
		return
	}
	if cfg.PruneAt > 0 && sess.lastPromptTokens >= cfg.PruneAt {
		pruneOldToolOutputs(cfg, sess)
	}
}

var errNothingToCompact = errors.New("nothing to compact")

// systemPromptShare is the percent of compact_at the system prompt may take
// before the operator is told: past half, compaction fights for the remainder.
const systemPromptShare = 50

// Compaction cannot reclaim the configured prompt, memory, or skill metadata.
// Warn once per session when that fixed overhead consumes the message budget.
func warnFixedOverhead(cfg TurnConfig, sess *Session) {
	if sess.warnedFixedOverhead || cfg.Log == nil {
		return
	}
	sysPrompt := renderSystemPrompt(cfg)
	fixed := estimatePromptTokens([]llm.Message{{Role: llm.RoleSystem, Content: sysPrompt}})
	if fixed < cfg.CompactAt*systemPromptShare/100 {
		return
	}
	sess.warnedFixedOverhead = true
	cfg.Log.Warn("system prompt is consuming the compaction budget; compaction cannot reclaim it",
		"system_prompt_tokens", fixed,
		"compact_at", cfg.CompactAt,
		"hint", "shorten the prompt, memory, or skill metadata in shell3.lisp")
}

// compactNow is the auto path: compactApply already logged any failure, so
// the result is deliberately discarded and the turn proceeds either way.
func compactNow(ctx context.Context, cfg TurnConfig, sess *Session) {
	_, _, _ = compactApply(ctx, cfg, sess)
}

// compactApply summarises the head and rebuilds history as that summary plus
// the verbatim tail. On any problem — too little history, an LLM error, an
// empty summary — it returns an error WITHOUT compacting, so callers proceed
// un-compacted and before == after. On success lastPromptTokens is reset to
// the rewritten history's size so the threshold is not re-tripped next turn.
func compactApply(ctx context.Context, cfg TurnConfig, sess *Session) (before, after int, err error) {
	before = estimatePromptTokens(sess.messages)
	// Tail boundary before the floor check: a history that fits within
	// keepRecent has nothing left to summarise.
	keepRecent := resolveKeepRecent(cfg)
	if keepRecent <= 0 {
		keepRecent = minimumKeepRecent
	}
	cut := compactionCut(sess.messages, keepRecent)
	if cut <= 0 || cut >= len(sess.messages) {
		// No head: the tail covers everything, or the snap-forward over a
		// trailing all-tool run ate it, and compacting would discard the
		// latest turn.
		return before, before, errNothingToCompact
	}
	head := sess.messages[:cut]
	// Skip only when the head is BOTH few messages AND few tokens — a short
	// head of giant tool results is exactly what should collapse.
	if cut < compactionFloor && estimatePromptTokens(head) < compactionFloorTokens {
		return before, before, errNothingToCompact
	}
	tail := sess.messages[cut:]

	// One quiet LLM call over the head we are about to discard, emitting no
	// events — the user must not see the summary stream as a turn response.
	compactMsgs := make([]llm.Message, 0, len(head)+1)
	compactMsgs = append(compactMsgs, llm.Message{Role: llm.RoleSystem, Content: compactionInstruction})
	compactMsgs = append(compactMsgs, head...)

	cfg.Log.Debug("auto-compaction starting", "head_msgs", len(head))
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

	summaryArgs := CompactSummary{Summary: summary}

	// compactInto rewrites sess.messages in place and rolls the store session;
	// RunTurn rebuilds its own allMsgs after maybeCompact returns.
	prevTokens := sess.lastPromptTokens
	if !compactInto(summaryArgs, cfg.Store, sess, tail, cfg.Log) {
		// Roll failed, history untouched: do not reset the gauge or emit a
		// misleading compacted event.
		return before, before, errors.New("runs-session roll failed; history untouched")
	}

	// Reset the gauge so the next turn does not re-trip the threshold before a
	// real usage count lands.
	newTokens := estimatePromptTokens(sess.messages)
	sess.lastPromptTokens = newTokens
	// The reminder tracker remembers the last emitted bucket across turns;
	// stale high values would suppress every context reminder as the
	// conversation re-grows through the same band.
	sess.reminders.resetContextGauge()

	emitCompacted(sess, prevTokens, newTokens)
	return before, newTokens, nil
}

// pruneOldToolOutputs stubs large tool results before the protected tail with
// no LLM call, mutating the in-memory slice only (the append-only store keeps
// originals). Idempotent: a stub is far below pruneMinBytes.
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

// streamQuiet is streamOnce's non-emitting sibling: one call, assistant text
// only, no chat.Events, so the compaction round-trip is invisible to the UI.
// Tool calls, reasoning and usage are discarded.
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

// msgTokens is (content + reasoning + tool-call args) / 4. Reasoning counts
// because the adapter re-sends it, so it occupies real prompt tokens the
// tail-sizing walk must not miss.
func msgTokens(m llm.Message) int {
	n := len(m.Content) + len(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		n += len(tc.RawArgs)
	}
	return n / 4
}

// estimatePromptTokens sums a slice. Pruning mutates in place, so freed
// context is accounted for automatically.
func estimatePromptTokens(msgs []llm.Message) int {
	var total int
	for _, m := range msgs {
		total += msgTokens(m)
	}
	return total
}

// compactionCut is where the preserved tail begins: the most recent messages
// summing to at least keepRecent, snapped FORWARD past any leading tool
// message, since a request carrying a tool result whose assistant tool_call is
// absent is rejected. Head is msgs[:cut], tail msgs[cut:]; keepRecent <= 0
// returns len(msgs).
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

// CompactSummary is one compaction's narrative product.
type CompactSummary struct {
	Summary string
}

// compactInto replaces history with the summary plus tail (the sub-slice of
// sess.messages to keep), rolling the runs session so the boundary is visible.
// Callers validate that args.Summary is non-empty.
//
// False, with the in-memory history untouched, when the session roll fails:
// rewriting memory to the short slice while the outgoing session's record
// still holds the full history would let the next saveHistory duplicate the
// tail into it. Aborting keeps the stored history coherent.
func compactInto(args CompactSummary, st *runs.Store, sess *Session, tail []llm.Message, lg applog.Logger) bool {
	previous := sess.id
	var body strings.Builder
	fmt.Fprintf(&body, "<system-reminder>\nContinuation of session %s. History compacted.\n</system-reminder>\n\n", previous)
	fmt.Fprintf(&body, "<compact-summary>\n%s\n</compact-summary>", args.Summary)
	messages := make([]llm.Message, 0, 1+len(tail))
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: body.String()})
	messages = append(messages, tail...)
	id, persisted := previous, 0
	if st != nil {
		if sess.persistedLen > len(sess.messages) {
			return false
		}
		var err error
		id, err = st.RollSession(previous, sess.messages[sess.persistedLen:], messages)
		if err != nil {
			lg.Warn("persist compaction failed; keeping original session", "error", err)
			return false
		}
		persisted = len(messages)
	}
	sess.msgMu.Lock()
	sess.id, sess.messages, sess.persistedLen = id, messages, persisted
	sess.reminderLog = nil
	sess.msgMu.Unlock()
	return true
}

// pruneMinBytes is the smallest tool result worth pruning. A ~30-byte stub is
// far below it, which is what makes pruneOldToolOutputs idempotent unflagged.
const pruneMinBytes = 2048

// pruneStub is the placeholder a pruned tool result is replaced with.
func pruneStub(stem string, origLen int) string {
	return fmt.Sprintf("[%s — original was %d bytes]", stem, origLen)
}
