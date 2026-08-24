package telegram

import (
	"testing"
	"time"
)

func TestPollHealthFirstErrorLogsImmediately(t *testing.T) {
	h := newPollHealth()
	if logNow, fails := h.fail(); !logNow || fails != 1 {
		t.Fatalf("first fail: logNow=%v fails=%d, want true/1", logNow, fails)
	}
}

func TestPollHealthThrottlesRepeatedErrors(t *testing.T) {
	h := newPollHealth()
	clock := time.Unix(1000, 0)
	h.now = func() time.Time { return clock }
	h.fail()
	if logNow, _ := h.fail(); logNow {
		t.Fatal("second immediate fail should be throttled")
	}
	clock = clock.Add(pollHealthLogEvery)
	if logNow, fails := h.fail(); !logNow || fails != 3 {
		t.Fatalf("after throttle window: logNow=%v fails=%d, want true/3", logNow, fails)
	}
}

func TestPollHealthRecoveryReportsOutage(t *testing.T) {
	h := newPollHealth()
	clock := time.Unix(1000, 0)
	h.now = func() time.Time { return clock }
	h.fail()
	h.fail()
	clock = clock.Add(17 * time.Minute)
	recovered, outage, fails := h.ok()
	if !recovered || outage != 17*time.Minute || fails != 2 {
		t.Fatalf("recovery: recovered=%v outage=%s fails=%d", recovered, outage, fails)
	}
	// Healthy state stays quiet.
	if recovered, _, _ := h.ok(); recovered {
		t.Fatal("ok while healthy must not report recovery")
	}
}

func TestPollHealthQuietRecoveryClosesOutage(t *testing.T) {
	h := newPollHealth()
	clock := time.Unix(1000, 0)
	h.now = func() time.Time { return clock }
	h.fail()
	clock = clock.Add(30 * time.Second)
	h.fail()

	// Still inside the quiet window: nothing to report yet.
	clock = clock.Add(pollQuietRecovery - time.Second)
	if recovered, _, _ := h.sweep(); recovered {
		t.Fatal("sweep inside the quiet window must not report recovery")
	}

	// Past it: the outage closed at its LAST error, not at detection time.
	clock = clock.Add(2 * time.Second)
	recovered, outage, fails := h.sweep()
	if !recovered || outage != 30*time.Second || fails != 2 {
		t.Fatalf("quiet recovery: recovered=%v outage=%s fails=%d, want true/30s/2", recovered, outage, fails)
	}
	// One recovery per outage.
	if recovered, _, _ := h.sweep(); recovered {
		t.Fatal("second sweep must not report the same outage twice")
	}
	// A healthy transport never sweeps into a recovery.
	if recovered, _, _ := h.sweep(); recovered {
		t.Fatal("sweep while healthy must not report recovery")
	}
}

func TestPollHealthQuietRecoveryThenNewOutage(t *testing.T) {
	h := newPollHealth()
	clock := time.Unix(1000, 0)
	h.now = func() time.Time { return clock }
	h.fail()
	clock = clock.Add(pollQuietRecovery + time.Second)
	if recovered, _, _ := h.sweep(); !recovered {
		t.Fatal("quiet recovery did not fire")
	}
	// The next error starts a FRESH outage — count restarts at 1 and logs.
	if logNow, fails := h.fail(); !logNow || fails != 1 {
		t.Fatalf("error after recovery: logNow=%v fails=%d, want true/1", logNow, fails)
	}
}
