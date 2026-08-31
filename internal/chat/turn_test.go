package chat

import "testing"

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
