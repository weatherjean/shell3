package chat

import "testing"

// A front-end that consumes assistant text programmatically (shell3 ask
// --agent, feeding a script's stdout) must be able to tell a cut-off reply
// from a complete one. The notice rides the reply itself — there is no error
// channel carrying it — so the text is the only place to look.
func TestIsTruncatedReply(t *testing.T) {
	if !IsTruncatedReply("here is the draft" + truncationNotice) {
		t.Fatal("a reply carrying the notice must report truncated")
	}
	if IsTruncatedReply("here is the draft") {
		t.Fatal("a clean reply must not report truncated")
	}
	if IsTruncatedReply("") {
		t.Fatal("empty text is not a truncated reply")
	}
}
