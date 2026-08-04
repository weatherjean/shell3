package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/runs"
)

// TestSetStoreSwapsReminderTarget covers the reload bug: a session's sidecar
// store handle must be repointable at a new *runs.Store (the new generation's
// open handle onto the SAME db file) without losing reminder writes once the
// old handle closes.
func TestSetStoreSwapsReminderTarget(t *testing.T) {
	dir := t.TempDir()
	st1, err := runs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st1.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(SessionOpts{StoreID: id, Store: st1})
	// Second handle to the same DB stands in for the reload's new generation.
	st2, err := runs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	s.SetStore(st2)
	if err := st1.Close(); err != nil {
		t.Fatal(err)
	}
	s.recordReminder("after swap")
	lines, err := st2.LoadReminders(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Text != "after swap" {
		t.Fatalf("reminder not persisted through swapped store: %+v", lines)
	}
}
