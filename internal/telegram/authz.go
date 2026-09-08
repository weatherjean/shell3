package telegram

import (
	"fmt"
	"strconv"
	"strings"
)

// senderAllowlist decides which Telegram users may drive the agent.
// Telegram supplies sender identity; membership in a chat grants no authority.
type senderAllowlist struct {
	ids map[int64]struct{}
}

// newSenderAllowlist builds the allowlist from configured ids, falling back to
// the chat owner when none are given.
//
// The fallback is what keeps every existing single-DM config working
// untouched: in a direct chat the chat id IS the user id, so "the owner of the
// configured chat" and "the person who has always been talking to this bot"
// are the same number. An operator who never opens a group never has to learn
// this setting exists.
func newSenderAllowlist(chatID string, ids []string) (*senderAllowlist, error) {
	a := &senderAllowlist{ids: make(map[int64]struct{}, len(ids)+1)}
	for _, raw := range ids {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("telegram: allow_from: %q is not a numeric user id", raw)
		}
		a.ids[id] = struct{}{}
	}
	if len(a.ids) == 0 {
		if id, err := strconv.ParseInt(chatID, 10, 64); err == nil && id > 0 {
			a.ids[id] = struct{}{}
		}
	}
	return a, nil
}

// allows reports whether senderID may drive the agent. A zero sender — a
// channel post, or a transport that cannot attribute the message — is never
// allowed: an unattributable message is one nobody can be held to.
func (a *senderAllowlist) allows(senderID int64) bool {
	if a == nil || senderID == 0 {
		return false
	}
	_, ok := a.ids[senderID]
	return ok
}
