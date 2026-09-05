//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func drainContextEvents(c *conversation, events ...shell3.Event) {
	ch := make(chan shell3.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	c.drainTurn(context.Background(), ch, nil)
}

func TestContextMilestonesAndCompactionReset(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	c := tconv(b)
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	drainContextEvents(c, shell3.Event{Kind: shell3.Usage, PromptTokens: 2048})
	drainContextEvents(c, shell3.Event{Kind: shell3.Done, PromptTokens: 2500})
	drainContextEvents(c, shell3.Event{Kind: shell3.Usage, PromptTokens: 3072})
	drainContextEvents(c, shell3.Event{Kind: shell3.Compacted, PromptTokens: 500})
	drainContextEvents(c, shell3.Event{Kind: shell3.Usage, PromptTokens: 2200})

	got := fc.sentTexts()
	if len(got) != 4 {
		t.Fatalf("milestone posts = %d, want 4: %v", len(got), got)
	}
	for i, want := range []string{"50% full", "75% full", "conversation compacted", "50% full"} {
		if !strings.Contains(got[i], want) {
			t.Errorf("post %d = %q, want %q", i, got[i], want)
		}
	}
	if !fc.lastSilent() {
		t.Fatal("context milestones must not ring")
	}
}
