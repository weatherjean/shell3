//go:build unix

package heartbeat

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestPromptCarriesChecklistAndConvention(t *testing.T) {
	hb := shell3.Heartbeat{Every: 30 * time.Minute, Checklist: "- check the mail\n- water the plants"}
	p := Prompt(hb)
	if !strings.Contains(p, "- water the plants") {
		t.Fatalf("prompt must carry the checklist, got %q", p)
	}
	if !strings.Contains(p, "HEARTBEAT_OK") {
		t.Fatalf("prompt must state the HEARTBEAT_OK convention, got %q", p)
	}
}

func TestPromptPreambleOverride(t *testing.T) {
	hb := shell3.Heartbeat{Checklist: "- x", Prompt: "Custom preamble."}
	p := Prompt(hb)
	if !strings.HasPrefix(p, "Custom preamble.") {
		t.Fatalf("override must replace the preamble, got %q", p)
	}
	if !strings.Contains(p, "- x") {
		t.Fatalf("checklist must still be appended, got %q", p)
	}
}

func TestActiveWindow(t *testing.T) {
	at := func(hhmm string) time.Time {
		tm, err := time.Parse("2006-01-02 15:04", "2026-07-15 "+hhmm)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cases := []struct {
		name     string
		from, to string
		now      time.Time
		want     bool
	}{
		{"no window is always active", "", "", at("03:00"), true},
		{"inside", "08:00", "23:00", at("12:00"), true},
		{"before", "08:00", "23:00", at("07:59"), false},
		{"from is inclusive", "08:00", "23:00", at("08:00"), true},
		{"to is exclusive", "08:00", "23:00", at("23:00"), false},
		{"overnight inside late", "22:00", "06:00", at("23:30"), true},
		{"overnight inside early", "22:00", "06:00", at("05:00"), true},
		{"overnight outside", "22:00", "06:00", at("12:00"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hb := shell3.Heartbeat{ActiveFrom: tc.from, ActiveTo: tc.to}
			if got := Active(hb, tc.now); got != tc.want {
				t.Fatalf("Active(%s-%s at %s) = %v, want %v", tc.from, tc.to, tc.now.Format("15:04"), got, tc.want)
			}
		})
	}
}

func TestActiveWindowTZ(t *testing.T) {
	// 12:00 UTC is 21:00 in Tokyo: inside a Tokyo evening window, outside a
	// UTC one.
	nowUTC := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	hb := shell3.Heartbeat{ActiveFrom: "20:00", ActiveTo: "23:00", TZ: "Asia/Tokyo"}
	if !Active(hb, nowUTC) {
		t.Fatal("12:00 UTC = 21:00 Tokyo must be inside a 20:00-23:00 Tokyo window")
	}
	hb.TZ = "UTC"
	if Active(hb, nowUTC) {
		t.Fatal("12:00 UTC must be outside a 20:00-23:00 UTC window")
	}
}

func TestStrip(t *testing.T) {
	cases := []struct {
		name, in, want string
		drop           bool
	}{
		{"just the token", "HEARTBEAT_OK", "", true},
		{"token with whitespace", "  HEARTBEAT_OK\n", "", true},
		{"bold token", "**HEARTBEAT_OK**", "", true},
		{"leading token", "HEARTBEAT_OK\nbut the disk is 95% full", "but the disk is 95% full", false},
		{"trailing token", "disk is 95% full\nHEARTBEAT_OK", "disk is 95% full", false},
		{"mid-sentence token stays", "the string HEARTBEAT_OK is a sentinel", "the string HEARTBEAT_OK is a sentinel", false},
		{"plain alert untouched", "the disk is 95% full", "the disk is 95% full", false},
		{"empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, drop := Strip(tc.in)
			if got != tc.want || drop != tc.drop {
				t.Fatalf("Strip(%q) = (%q, %v), want (%q, %v)", tc.in, got, drop, tc.want, tc.drop)
			}
		})
	}
}

// tickerHarness runs a Ticker at a tiny interval and records injections.
type tickerHarness struct {
	mu       sync.Mutex
	injected []string
	now      time.Time
}

func (h *tickerHarness) inject(p string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.injected = append(h.injected, p)
}

func (h *tickerHarness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.injected)
}

func TestTickerInjectsWhenIdle(t *testing.T) {
	h := &tickerHarness{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	hb := shell3.Heartbeat{Every: 5 * time.Millisecond, Checklist: "- x"}
	tk := NewTicker(hb, h.inject, func() bool { return false })
	tk.now = func() time.Time { return h.now }
	tk.Start()
	defer tk.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for h.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.count() == 0 {
		t.Fatal("idle ticker never injected a heartbeat prompt")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !strings.Contains(h.injected[0], "- x") {
		t.Fatalf("injected prompt must carry the checklist, got %q", h.injected[0])
	}
}

func TestTickerSkipsWhenBusy(t *testing.T) {
	h := &tickerHarness{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
	hb := shell3.Heartbeat{Every: time.Millisecond, Checklist: "- x"}
	tk := NewTicker(hb, h.inject, func() bool { return true })
	tk.now = func() time.Time { return h.now }
	tk.Start()
	time.Sleep(30 * time.Millisecond)
	tk.Stop()
	if n := h.count(); n != 0 {
		t.Fatalf("busy ticker must not inject, got %d injections", n)
	}
}

func TestTickerSkipsOutsideActiveHours(t *testing.T) {
	h := &tickerHarness{now: time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)} // 03:00
	hb := shell3.Heartbeat{Every: time.Millisecond, Checklist: "- x", ActiveFrom: "08:00", ActiveTo: "23:00", TZ: "UTC"}
	tk := NewTicker(hb, h.inject, func() bool { return false })
	tk.now = func() time.Time { return h.now }
	tk.Start()
	time.Sleep(30 * time.Millisecond)
	tk.Stop()
	if n := h.count(); n != 0 {
		t.Fatalf("ticker must not inject outside active hours, got %d injections", n)
	}
}
