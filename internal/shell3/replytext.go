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
