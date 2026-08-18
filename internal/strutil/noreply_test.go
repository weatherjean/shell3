package strutil

import "testing"

func TestIsNoReply(t *testing.T) {
	yes := []string{"NO_REPLY", "no_reply", " NO_REPLY. ", "*NO_REPLY*", "", "_REPLY", "REPLY"}
	for _, s := range yes {
		if !IsNoReply(s) {
			t.Errorf("IsNoReply(%q) = false, want true", s)
		}
	}
	no := []string{"NO", "ok", "NO_REPLY is what I would say, but here is the report"}
	for _, s := range no {
		if IsNoReply(s) {
			t.Errorf("IsNoReply(%q) = true, want false", s)
		}
	}
}
