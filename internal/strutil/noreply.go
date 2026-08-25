package strutil

import "strings"

// NoReplySentinel is the reply a mail turn uses to say "nothing to post".
const NoReplySentinel = "NO_REPLY"

// IsNoReply reports whether a model reply is the no-post sentinel. Matching is
// lenient by design: a reasoning-split provider can swallow the leading "NO"
// into reasoning_content and deliver only the tail (observed live: the model
// said NO_REPLY, the host received "_REPLY"), so any 4+-character tail
// fragment of the sentinel counts — no real reply is ever just "REPLY". An
// empty reply is also silence.
func IsNoReply(reply string) bool {
	reply = strings.Trim(strings.TrimSpace(reply), ".!`\"'* \n")
	if reply == "" {
		return true
	}
	up := strings.ToUpper(reply)
	return up == NoReplySentinel || (len(up) >= 4 && strings.HasSuffix(NoReplySentinel, up))
}

// StripNoReply removes a trailing NO_REPLY line from a reply, returning the
// remaining text and whether the sentinel was there.
//
// A model that has something to say AND wants to end quietly writes both:
// observed live 2026-08-25, the reply was "Background dry-run dispatched. Will
// land next turn.\n\nNO_REPLY", and because IsNoReply only matches when the
// WHOLE reply is the sentinel, all of it posted — the sentinel included, in
// the user's chat, as if it were part of the message.
//
// Only a trailing, standalone line is stripped. Prose that merely mentions the
// sentinel — this file's own documentation, an agent explaining its contract —
// keeps it, because there the word is content rather than a signal.
//
// The remaining text is what callers post; an empty remainder is silence, and
// IsNoReply already reports true for it.
func StripNoReply(reply string) (text string, had bool) {
	trimmed := strings.TrimRight(reply, " \t\n")
	idx := strings.LastIndexByte(trimmed, '\n')
	last := trimmed[idx+1:] // whole string when there is no newline
	if !IsNoReply(last) || strings.TrimSpace(last) == "" {
		return reply, false
	}
	if idx < 0 {
		return "", true // the reply was only the sentinel
	}
	return strings.TrimRight(trimmed[:idx], " \t\n"), true
}
