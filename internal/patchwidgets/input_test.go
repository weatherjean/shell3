package patchwidgets

import "testing"

func TestParseKey(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want keyKind
	}{
		{"ascii", []byte("a"), keyChar},
		{"enter", []byte{13}, keyEnter},
		{"newline", []byte{10}, keyEnter},
		{"backspace", []byte{127}, keyBackspace},
		{"ctrlc", []byte{3}, keyCtrlC},
		{"ctrlu", []byte{21}, keyCtrlU},
		{"ctrlw", []byte{23}, keyCtrlW},
		{"esc", []byte{27}, keyEscape},
		{"tab", []byte{9}, keyTab},
		{"up", []byte{27, '[', 'A'}, keyUp},
		{"down", []byte{27, '[', 'B'}, keyDown},
		{"right", []byte{27, '[', 'C'}, keyRight},
		{"left", []byte{27, '[', 'D'}, keyLeft},
		{"home", []byte{27, '[', 'H'}, keyHome},
		{"end", []byte{27, '[', 'F'}, keyEnd},
		{"shift-tab", []byte{27, '[', 'Z'}, keyShiftTab},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := parseKey(tc.in)
			if k.kind != tc.want {
				t.Fatalf("got %v want %v", k.kind, tc.want)
			}
		})
	}
}

func TestParseKeyChar(t *testing.T) {
	k, n := parseKey([]byte("x"))
	if k.kind != keyChar || k.r != 'x' || n != 1 {
		t.Fatalf("got %+v n=%d", k, n)
	}
}

func TestParseKeyUTF8(t *testing.T) {
	k, n := parseKey([]byte("é")) // 2-byte
	if k.kind != keyChar || k.r != 'é' || n != 2 {
		t.Fatalf("got %+v n=%d", k, n)
	}
}

func TestTrimToWord(t *testing.T) {
	got := trimToWord([]rune("hello world"))
	if string(got) != "hello " {
		t.Fatalf("got %q", string(got))
	}
	got = trimToWord([]rune("hello   "))
	if string(got) != "" {
		t.Fatalf("got %q", string(got))
	}
}

func TestFilteredIndexes(t *testing.T) {
	choices := []PickChoice{{Value: "alpha"}, {Value: "beta"}, {Value: "gamma", Hint: "alphabet"}}
	got := filteredIndexes(choices, "alpha")
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
	got = filteredIndexes(choices, "")
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestIndexOfDefault(t *testing.T) {
	choices := []PickChoice{{Value: "a"}, {Value: "b"}, {Value: "c"}}
	if got := indexOfDefault(choices, "b"); got != 1 {
		t.Fatalf("got %d", got)
	}
	if got := indexOfDefault(choices, ""); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := indexOfDefault(choices, "missing"); got != 0 {
		t.Fatalf("got %d", got)
	}
}
