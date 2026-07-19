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
