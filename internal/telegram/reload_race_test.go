//go:build unix

package telegram

import (
	"context"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/media"
)

// A reload rewires the bot's media capabilities and cron job runner. It can run
// on a TURN goroutine (the agent's reload tool applies at end of turn, after the
// turn slot is released) while the update loop is concurrently handling
// commands that read the guarded fields (e.g. /run reads runJob). Nothing
// serializes the two, so they must be guarded by b.mu.
func TestReloadRacesCommandHandling(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	caps := func() *MediaCaps {
		return &MediaCaps{
			Clients: media.Clients{Transcribe: func(context.Context, string) (string, error) {
				return "", nil
			}},
			STTEcho: true,
		}
	}
	b.SetMedia(caps())
	b.SetJobRunner(func(string) error { return nil })

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the reload path
		defer wg.Done()
		for range 200 {
			b.SetMedia(caps())
			b.SetJobRunner(func(string) error { return nil })
		}
	}()
	go func() { // the update loop handling commands
		defer wg.Done()
		for range 200 {
			b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/status"})
			b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/run nightly"})
		}
	}()
	wg.Wait()
}
