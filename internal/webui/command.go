//go:build unix

package webui

// Slash commands: messages the front-end answers itself rather than sending to
// the model. They run on the same turn gate as a real message, because they act
// on the same conversation — a compaction rewriting history while a turn reads
// it would corrupt both.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/shell3"
)

// commandName returns the slash command a message invokes, or "" when the
// message is ordinary text. Only an exact, argument-free command counts:
// "/compact" is a command, "/compact the logs please" is a sentence about one.
func commandName(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t\n") {
		return ""
	}
	return strings.ToLower(trimmed[1:])
}

// runCommand answers a slash command into the open stream, reporting whether
// the message was one. An unknown command is not handled here — it goes to the
// model like any other text, which is the friendlier reading of a typo.
func (s *Server) runCommand(ctx context.Context, stream *streamWriter, sess *shell3.Session, prompt string) bool {
	switch commandName(prompt) {
	case "compact":
		stream.delta("text", s.compactNow(ctx, sess))
		return true
	default:
		return false
	}
}

// compactNow summarises the conversation's head and reports what it freed. The
// numbers are estimates from the same estimator on both sides, so the delta is
// meaningful even though neither figure is exact.
func (s *Server) compactNow(ctx context.Context, sess *shell3.Session) string {
	before, after, err := sess.Compact(ctx)
	switch {
	case errors.Is(err, chat.ErrNothingToCompact):
		return "Nothing to compact yet — the whole conversation still fits in the part kept verbatim."
	case err != nil:
		return "Compaction failed: " + err.Error() + "\n\nThe conversation is unchanged."
	}
	freed := before - after
	if freed <= 0 {
		return fmt.Sprintf("Compacted. Context is about %s.", tokenCount(after))
	}
	return fmt.Sprintf("Compacted: about %s → %s (%s freed). The recent turns are kept verbatim; everything older is now a summary.",
		tokenCount(before), tokenCount(after), tokenCount(freed))
}

// tokenCount renders an estimated token count at a resolution the estimate
// actually supports.
func tokenCount(tokens int) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d tokens", tokens)
	}
	return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
}
