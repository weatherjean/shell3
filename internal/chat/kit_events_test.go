package chat

import (
	"slices"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

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
