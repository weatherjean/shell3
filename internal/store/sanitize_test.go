package store

import "testing"

func TestSanitizeFTSQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", `"hello"`},
		{"hello world", `"hello" "world"`},
		{"cobra colorful cli ?", `"cobra" "colorful" "cli"`},
		{"a:b c-d", `"a:b" "c-d"`},
		{`he said "go"`, `"he" "said" """go"""`},
		{"", ""},
		{"  ", ""},
		{"?!.,", ""},
	}
	for _, tc := range cases {
		got := sanitizeFTSQuery(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
