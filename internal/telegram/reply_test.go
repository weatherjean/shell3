//go:build unix

package telegram

import "testing"

func TestWithReplyContext(t *testing.T) {
	got := withReplyContext("what does this do?", "func foo() {\n  return 1\n}")
	want := "> func foo() {\n>   return 1\n> }\n\nwhat does this do?"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	if withReplyContext("hi", "") != "hi" {
		t.Fatal("no reply text should pass through unchanged")
	}
	if withReplyContext("hi", "   ") != "hi" {
		t.Fatal("blank reply text should pass through unchanged")
	}
}
