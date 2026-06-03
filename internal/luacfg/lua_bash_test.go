package luacfg

import (
	"testing"
	"time"
)

func TestWithIOUnlockTopLevelLeavesForeignLockHeld(t *testing.T) {
	c := &LoadedConfig{}

	// Goroutine B holds the VM lock (as a concurrent CallTool would). This
	// deliberately breaks the single-agent invariant to prove the fix is robust
	// rather than corrupting: a top-level withIOUnlock (c.vmLockHeld == false)
	// must NOT touch a mutex it does not own.
	bHolds := make(chan struct{})
	bRelease := make(chan struct{})
	bDone := make(chan struct{})
	go func() {
		c.mu.Lock()
		close(bHolds)
		<-bRelease
		c.mu.Unlock()
		close(bDone)
	}()
	<-bHolds

	// c.vmLockHeld is false: this call is NOT inside CallTool/runLuaGuard.
	heldDuringF := false
	c.withIOUnlock(func() {
		// Probe whether the lock is still held by B. TryLock succeeds only if
		// the lock is free — which would mean withIOUnlock wrongly released the
		// lock B holds (the TryLock-ownership-inference bug).
		if c.mu.TryLock() {
			c.mu.Unlock()
		} else {
			heldDuringF = true
		}
	})

	close(bRelease)
	<-bDone
	if !heldDuringF {
		t.Fatal("withIOUnlock released a lock held by another goroutine (TryLock ownership-inference bug)")
	}
}

// TestWithIOUnlockReleasesAndReacquiresWhenHeld is the positive-path companion:
// when this goroutine genuinely holds the VM lock (vmLockHeld == true, as inside
// CallTool/runLuaGuard), withIOUnlock MUST release c.mu around f() so other work
// can proceed, then reacquire it. A no-op withIOUnlock would fail this.
func TestWithIOUnlockReleasesAndReacquiresWhenHeld(t *testing.T) {
	c := &LoadedConfig{}

	// Simulate being inside CallTool/runLuaGuard: we hold c.mu and the flag set.
	c.mu.Lock()
	c.vmLockHeld = true

	// A waiter that can only acquire c.mu if withIOUnlock actually releases it
	// during f().
	acquired := make(chan struct{})
	go func() {
		c.mu.Lock()
		close(acquired)
		c.mu.Unlock()
	}()

	releasedDuringF := false
	c.withIOUnlock(func() {
		select {
		case <-acquired:
			releasedDuringF = true
		case <-time.After(2 * time.Second):
		}
	})

	if !c.vmLockHeld {
		t.Fatal("withIOUnlock did not restore vmLockHeld after reacquiring c.mu")
	}
	// Release the lock we (re)hold to finish cleanly.
	c.vmLockHeld = false
	c.mu.Unlock()

	if !releasedDuringF {
		t.Fatal("withIOUnlock did not release c.mu during f() when the lock was held")
	}
}
