package patchapp

import "testing"

func TestParseInput_UTF8Printable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want rune
		used int
	}{
		{name: "em dash", in: "—", want: '—', used: 3},
		{name: "curly apostrophe", in: "’", want: '’', used: 3},
		{name: "arrow", in: "→", want: '→', used: 3},
		{name: "prompt symbol", in: "➜", want: '➜', used: 3},
		{name: "emoji", in: "👋", want: '👋', used: 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, used := parseInput([]byte(tc.in))
			if got.kind != keyChar || got.r != tc.want || used != tc.used {
				t.Fatalf("parseInput(%q) = (%v, %q), %d; want keyChar %q, %d", tc.in, got.kind, got.r, used, tc.want, tc.used)
			}
		})
	}
}

func TestParseInput_EscapeStillWorks(t *testing.T) {
	got, used := parseInput([]byte{27})
	if got.kind != keyEscape || used != 1 {
		t.Fatalf("parseInput(ESC) = (%v, %q), %d; want keyEscape, 1", got.kind, got.r, used)
	}
}

func TestProcessInput_BracketedPastePreservesUTF8(t *testing.T) {
	app := New("test", "", WelcomeInfo{})
	app.r.SetOutput(discardWriter{})

	app.processInput([]byte(pasteStart + "— ’ → ➜ 👋\rline" + pasteEnd))

	want := "— ’ → ➜ 👋\nline"
	if got := string(app.input); got != want {
		t.Fatalf("pasted input = %q; want %q", got, want)
	}
}

func TestProcessInput_SplitUTF8AndPasteEnd(t *testing.T) {
	app := New("test", "", WelcomeInfo{})
	app.r.SetOutput(discardWriter{})

	body := []byte(pasteStart + "—" + pasteEnd)
	app.processInput(body[:len(pasteStart)+1])
	app.processInput(body[len(pasteStart)+1 : len(body)-2])
	app.processInput(body[len(body)-2:])

	if got, want := string(app.input), "—"; got != want {
		t.Fatalf("split pasted input = %q; want %q", got, want)
	}
	if len(app.inputPending) != 0 {
		t.Fatalf("inputPending not drained: %q", string(app.inputPending))
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
