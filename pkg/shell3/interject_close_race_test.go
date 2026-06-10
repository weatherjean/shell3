package shell3

import (
	"sync"
	"testing"
)

// TestSession_InterjectCloseRace exercises the documented contract that
// Interject is callable from any goroutine while another goroutine calls
// Close. wake() (reached from Interject on an idle session) reads s.runtime,
// which doClose nils under s.mu — the read must take the same lock or
// `go test -race` flags a data race on the s.runtime pointer.
//
// Many concurrent Interject goroutines overlap a single Close to force the
// interleaving where wake() observes s.runtime mid-nil.
func TestSession_InterjectCloseRace(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	s, err := rt.Session(SessionOpts{Name: "race"})
	if err != nil {
		t.Fatal(err)
	}

	const interjectors = 32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < interjectors; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// The session is idle, so Interject's !isBusy() branch calls
			// wake(), which reads s.runtime concurrently with Close's nil.
			for j := 0; j < 50; j++ {
				s.Interject("steer")
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	close(start) // release all goroutines as simultaneously as possible
	wg.Wait()

	// Interject after Close must remain a safe no-op (snapshotted nil runtime).
	s.Interject("after close")
}
