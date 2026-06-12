package patchmd

import (
	"strings"
	"testing"
)

// FuzzApplyInline exercises the code-span/link/emphasis tokenizer for panics and
// for the ANSI-leak bug class that TestInline_CodeAndLink_NoLeak pins: a 24-bit
// SGR body ("38;2;...") must never escape into the visible text. The check is
// relative to the input so fuzz strings that already contain "38;2;" don't
// false-positive. Plain text (no markdown metacharacters and no escape) must
// pass through unchanged.
func FuzzApplyInline(f *testing.F) {
	f.Add("plain text")
	f.Add("a `code` b")
	f.Add("see [docs](https://x.io) here")
	f.Add("**bold [link](u) end**")
	f.Add("***x*** ~~y~~ `z`")
	f.Add("[](#)")

	f.Fuzz(func(t *testing.T, s string) {
		out := applyInline(s) // must never panic

		// The SGR body must not leak as visible text unless the input already had it.
		if !strings.Contains(s, "38;2;") && strings.Contains(stripANSI(out), "38;2;") {
			t.Fatalf("SGR body leaked into visible text:\n in=%q\nout=%q", s, out)
		}

		// Plain text — no markdown metacharacters, no escape — is returned verbatim.
		if !strings.ContainsAny(s, "`*~[]()\033") && out != s {
			t.Fatalf("plain text mangled:\n in=%q\nout=%q", s, out)
		}
	})
}
