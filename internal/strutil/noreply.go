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
