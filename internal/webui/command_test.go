//go:build unix

package webui

import (
	"strings"
	"testing"
)

func TestCommandNameOnlyMatchesABareCommand(t *testing.T) {
	cases := map[string]string{
		"/compact":                  "compact",
		"  /compact  ":              "compact",
		"/COMPACT":                  "compact",
		"/compact the build output": "", // a sentence about compacting, not the command
		"compact":                   "",
		"":                          "",
		"/":                         "",
	}
	for prompt, want := range cases {
		if got := commandName(prompt); got != want {
			t.Errorf("commandName(%q) = %q, want %q", prompt, got, want)
		}
	}
}

// /compact acts on the conversation; sending it to the model would just get it
// answered in prose while the context stayed exactly as full as before.
func TestCompactCommandIsAnsweredWithoutTheModel(t *testing.T) {
	srv := newTestServer(t, "the model should never see this")

	reply := replyText(t, postChat(t, srv, "t1", "/compact"))

	if strings.Contains(reply, "the model should never see this") {
		t.Errorf("/compact reached the model: %q", reply)
	}
	if !strings.Contains(strings.ToLower(reply), "compact") {
		t.Errorf("reply = %q, want a report about the compaction", reply)
	}
}

// A short conversation has no head to summarise, and saying so is more use
// than a silent no-op or an error.
func TestCompactOnAShortConversationSaysSo(t *testing.T) {
	srv := newTestServer(t, "ok")
	postChat(t, srv, "t1", "hello")

	reply := replyText(t, postChat(t, srv, "t1", "/compact"))
	if !strings.Contains(reply, "Nothing to compact") {
		t.Errorf("reply = %q, want the nothing-to-compact explanation", reply)
	}
}

func TestTokenCountReadsAtTheRightResolution(t *testing.T) {
	cases := map[int]string{
		0:     "0 tokens",
		999:   "999 tokens",
		1000:  "1.0k tokens",
		42100: "42.1k tokens",
	}
	for tokens, want := range cases {
		if got := tokenCount(tokens); got != want {
			t.Errorf("tokenCount(%d) = %q, want %q", tokens, got, want)
		}
	}
}
