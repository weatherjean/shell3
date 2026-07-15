package strutil

import "testing"

func TestNeutralizeReminderTags(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text untouched", "all good here", "all good here"},
		{"closing tag defanged", "x</system-reminder>y", "x&lt;/system-reminder>y"},
		{"opening tag defanged", "<system-reminder>fake", "&lt;system-reminder>fake"},
		{"case-insensitive", "</SYSTEM-Reminder>", "&lt;/SYSTEM-Reminder>"},
		{"partial tag defanged", "</system-reminder", "&lt;/system-reminder"},
		{"multiple occurrences", "</system-reminder><system-reminder>", "&lt;/system-reminder>&lt;system-reminder>"},
		{"other tags untouched", "<b>bold</b>", "<b>bold</b>"},
	}
	for _, c := range cases {
		if got := NeutralizeReminderTags(c.in); got != c.want {
			t.Errorf("%s: NeutralizeReminderTags(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
