package strutil

import "strings"

// NoReplySentinel is the reply a mail turn uses to say "nothing to post".
const NoReplySentinel = "NO_REPLY"

// IsNoReply reports whether a model reply is the no-post sentinel. It accepts
// 4+-character suffixes because reasoning-split providers may consume the
// leading fragment. An empty reply is also silence.
func IsNoReply(reply string) bool {
	reply = strings.Trim(strings.TrimSpace(reply), ".!`\"'* \n")
	if reply == "" {
		return true
	}
	up := strings.ToUpper(reply)
	return up == NoReplySentinel || (len(up) >= 4 && strings.HasSuffix(NoReplySentinel, up))
}

// StripNoReply removes only a trailing, standalone NO_REPLY line. Mentions in
// prose remain content.
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
