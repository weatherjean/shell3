package shell3

import (
	"fmt"
	"strings"
)

// Shared chat-reply rendering for the web front-end: the wording lives in one
// place next to the APIs it describes.

// ReloadReplyText renders a reload coordinator's result as the chat reply.
func ReloadReplyText(res ReloadResult, err error) string {
	if err != nil {
		return "\u274c reload failed: " + err.Error()
	}
	msg := fmt.Sprintf("\u2705 reloaded \u2014 %d agents, %d models, %d jobs", res.Agents, res.Models, res.Jobs)
	if len(res.Notes) > 0 {
		msg += "\n\u2022 " + strings.Join(res.Notes, "\n\u2022 ")
	}
	return msg
}

// StopReplyText renders a /stop outcome: whether a running turn was cancelled
// and how many background jobs were killed.
func StopReplyText(cancelled bool, killed int) string {
	switch {
	case cancelled && killed > 0:
		return fmt.Sprintf("\u23f9 stopped \u2014 killed %d background job(s)", killed)
	case cancelled:
		return "\u23f9 stopped"
	case killed > 0:
		return fmt.Sprintf("\u23f9 no turn running \u2014 killed %d background job(s)", killed)
	default:
		return "nothing running"
	}
}
