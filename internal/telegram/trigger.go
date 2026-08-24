//go:build unix

package telegram

import "strings"

// trigger.go decides whether a message is ADDRESSED to the bot.
//
// In a private chat the answer is always yes: there is nobody else in the
// room. In a group it is yes only for an @mention of this bot or a reply to
// one of the bot's own messages — a group is a room full of people talking to
// each other, and treating that as prompt input would both burn tokens on
// other people's conversation and put words the bot was never shown into its
// context.
//
// This is enforcement, not convenience. Telegram's own privacy mode cannot do
// it: a privacy-mode bot is never delivered a plain "@bot do X" text message
// at all (only /commands and replies), so supporting @mentions requires
// privacy OFF — which means the group's traffic reaches this process and it is
// this function that discards it.

// setGroup records whether this room is a group from the transport's chat
// type. A private chat needs no trigger; anything else does.
func (c *conversation) setGroup(chatType string) {
	group := chatType != "" && chatType != "private"
	c.mu.Lock()
	c.isGroup = group
	c.mu.Unlock()
}

// addressed reports whether m is aimed at this bot: an @mention, or a reply
// to something the bot itself sent. botUser is the bot's own @username
// without the "@" ("" when the transport could not say, which leaves replies
// as the only trigger).
func (c *conversation) addressed(m Msg, botUser string) bool {
	c.mu.Lock()
	group := c.isGroup
	c.mu.Unlock()
	if !group {
		return true
	}
	// Telegram's own answer first: it survives a restart. The remembered
	// sent-ids are a fallback for transports that cannot attribute a
	// replied-to message (the console dev transport).
	if m.ReplyToBot || c.wasSent(m.ReplyToID) {
		return true
	}
	return mentions(m.Text, botUser)
}

// mentions reports whether text @-mentions user. The match is
// case-insensitive (Telegram usernames are) and requires a boundary after the
// name, so "@mybottom" is not a mention of "mybot" — a lookalike username in
// the same room must not be able to drive this bot.
func mentions(text, user string) bool {
	if user == "" {
		return false
	}
	lowText, lowUser := strings.ToLower(text), strings.ToLower(user)
	for i := 0; ; {
		at := strings.Index(lowText[i:], "@"+lowUser)
		if at < 0 {
			return false
		}
		end := i + at + 1 + len(lowUser)
		if end >= len(lowText) || !isUsernameByte(lowText[end]) {
			return true
		}
		i = end
	}
}

// isUsernameByte reports whether ch may appear inside a Telegram username
// (letters, digits, underscore) — the boundary test mentions relies on.
func isUsernameByte(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_':
		return true
	}
	return false
}
