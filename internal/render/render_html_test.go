package render

import (
	"strings"
	"testing"
)

// The Rooms section is how the dash answers "which conversations is this bot
// holding" — one row per live room, each linked to its own transcript.
func TestRoomsSectionHTML(t *testing.T) {
	got := RoomsSectionHTML([]RoomInfo{
		{ChatID: -100, Title: "backend-infra", Busy: true, Jobs: 2, Queued: 1, SessionID: "s1"},
		{ChatID: 42, Title: "", SessionID: "s2"},
	}, "tok")
	for _, want := range []string{"backend-infra", "-100", "busy", "idle", "(untitled)", `/runs/s1?t=tok`, `/runs/s2?t=tok`} {
		if !strings.Contains(got, want) {
			t.Errorf("rooms section missing %q:\n%s", want, got)
		}
	}
}

// No rooms renders nothing: an empty table reads as breakage.
func TestRoomsSectionHTMLEmpty(t *testing.T) {
	if got := RoomsSectionHTML(nil, "tok"); got != "" {
		t.Fatalf("empty rooms rendered %q", got)
	}
}

// A room title comes from Telegram and is attacker-adjacent text; it must be
// escaped like everything else on the dash.
func TestRoomsSectionHTMLEscapes(t *testing.T) {
	got := RoomsSectionHTML([]RoomInfo{{ChatID: 1, Title: "<script>x</script>", SessionID: "s"}}, "t")
	if strings.Contains(got, "<script>") {
		t.Fatalf("room title was not escaped:\n%s", got)
	}
}
