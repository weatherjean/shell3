package shell3

import (
	"sync"
	"testing"
)

func TestSession_InterjectCloseRace(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	s, err := rt.Session(SessionOpts{})
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

	s.Interject("after close")
}
