package chat

import (
	"slices"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

// kit.EventNames is the list an `event:` block may subscribe to. It cannot be
// derived from EventKind at runtime without internal/kit importing the
// runtime, so this test is the pin: adding an EventKind without adding its
// name here (or vice versa) fails the build's tests rather than silently
// shipping a kind nobody can subscribe to.
//
// EventSessionStart is deliberately absent from kit.EventNames — nothing
// emits it any more; it exists so a zero Event is not mistaken for a real one.
func TestKitEventNamesMatchEventKinds(t *testing.T) {
	var emitted []string
	for k := EventSessionStart; k < numEventKinds; k++ {
		if k == EventSessionStart {
			continue
		}
		emitted = append(emitted, k.String())
	}
	for _, name := range emitted {
		if !slices.Contains(kit.EventNames, name) {
			t.Errorf("chat emits %q but kit.EventNames does not list it — an event: block cannot subscribe to it", name)
		}
	}
	for _, name := range kit.EventNames {
		if !slices.Contains(emitted, name) {
			t.Errorf("kit.EventNames lists %q but no EventKind stringifies to it — a subscriber to it would never fire", name)
		}
	}
}
