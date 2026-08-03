//go:build unix

package telegram

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/media"
)

// A reload rewires the bot's media capabilities and cron job runner. It can run
// on a TURN goroutine (the agent's reload tool applies at end of turn, after the
// turn slot is released) while the update loop is handling a command that reads
// exactly those fields — /voice reads media + voiceMode, /run reads runJob.
// Nothing serializes the two, so they must be guarded.
func TestReloadRacesCommandHandling(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	store := &ModeStore{Path: filepath.Join(t.TempDir(), "voice_mode.json")}
	caps := func() *MediaCaps {
		return &MediaCaps{
			Clients: media.Clients{Speak: func(context.Context, string) (media.Speech, error) {
				return media.Speech{}, nil
			}},
			TTSMode: "inbound",
		}
	}
	b.SetMedia(caps(), store)
	b.SetJobRunner(func(string) error { return nil })

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the reload path
		defer wg.Done()
		for range 200 {
			b.SetMedia(caps(), store)
			b.SetJobRunner(func(string) error { return nil })
		}
	}()
	go func() { // the update loop handling commands
		defer wg.Done()
		for range 200 {
			b.handleCommand(ctx, Msg{ChatID: 42, Text: "/voice"})
			b.handleCommand(ctx, Msg{ChatID: 42, Text: "/run nightly"})
		}
	}()
	wg.Wait()
}
