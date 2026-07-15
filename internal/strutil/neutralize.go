package strutil

import "regexp"

// reminderTag matches an opening or closing <system-reminder …> tag prefix,
// case-insensitively, so embedded variants like "</SYSTEM-Reminder>" are also
// caught. Matching just the prefix (no trailing ">") is deliberate: a partial
// or attribute-carrying tag is as dangerous as a complete one.
var reminderTag = regexp.MustCompile(`(?i)<(/?)(system-reminder)`)

// NeutralizeReminderTags defangs <system-reminder> envelope tags embedded in
// untrusted text (tool output, subagent summaries, user interjections) before
// that text is interpolated INTO a real <system-reminder> block. The leading
// "<" is HTML-escaped so the model still sees the original text's intent but
// cannot close the host's envelope or forge a new one.
func NeutralizeReminderTags(s string) string {
	return reminderTag.ReplaceAllString(s, "&lt;${1}${2}")
}
