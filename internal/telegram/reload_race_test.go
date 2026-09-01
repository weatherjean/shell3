//go:build unix

package telegram

import (
	"context"
	"sync"
	"testing"
)

func TestReloadRacesCommandHandling(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	b.SetJobRunner(func(string) error { return nil })

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the reload path
		defer wg.Done()
		for range 200 {
			b.SetJobRunner(func(string) error { return nil })
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/status"})
			tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/run nightly"})
		}
	}()
	wg.Wait()
}
