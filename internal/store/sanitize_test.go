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

func TestBuildFTSExpr(t *testing.T) {
	cases := []struct {
		terms    []string
		matchAll bool
		want     string
	}{
		{[]string{"cobra"}, false, `"cobra"`},
		{[]string{"cobra", "colorful"}, false, `"cobra" OR "colorful"`},
		{[]string{"cobra", "colorful"}, true, `"cobra" AND "colorful"`},
		{[]string{"a:b", "c-d"}, false, `"a:b" OR "c-d"`},
		{[]string{"hello world", "foo"}, false, `"hello world" OR "foo"`},
		{[]string{"  ", "?", "real"}, false, `"real"`},
		{[]string{}, false, ""},
		{[]string{"   "}, false, ""},
	}
	for _, tc := range cases {
		got := BuildFTSExpr(tc.terms, tc.matchAll)
		if got != tc.want {
			t.Errorf("BuildFTSExpr(%v, %v) = %q, want %q", tc.terms, tc.matchAll, got, tc.want)
		}
	}
}
