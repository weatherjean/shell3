package strutil

import "testing"

func TestStripNoReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
		had  bool
	}{
		{"content then sentinel", "Dispatched. Will land next turn.\n\nNO_REPLY",
			"Dispatched. Will land next turn.", true},
		{"bare sentinel", "NO_REPLY", "", true},
		{"bare sentinel padded", "  NO_REPLY\n\n", "", true},
		{"split fragment", "done\n_REPLY", "done", true},
		{"mentioned inline", "reply with NO_REPLY when nothing matters", "reply with NO_REPLY when nothing matters", false},
		{"mentioned then more", "NO_REPLY means silence.\nHere is the answer.", "NO_REPLY means silence.\nHere is the answer.", false},
		{"ordinary reply", "all done", "all done", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, had := StripNoReply(tc.in)
			if got != tc.want || had != tc.had {
				t.Fatalf("StripNoReply(%q) = %q,%v; want %q,%v", tc.in, got, had, tc.want, tc.had)
			}
		})
	}
}
