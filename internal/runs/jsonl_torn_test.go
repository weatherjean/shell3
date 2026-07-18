package runs

import (
	"os"
	"path/filepath"
	"testing"
)

// A crash mid-append can leave a partial record with no terminating newline.
// The next append must NOT fuse onto it (which would later fail the whole-file
// strict decode and drop all history) — appendLine heals the torn tail first.
func TestAppendLine_HealsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")

	// Two clean records.
	if err := appendLine(path, "message", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := appendLine(path, "message", map[string]int{"n": 2}); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-write: a partial record with no trailing newline.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"n":3,"partia`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// A further append must heal the torn tail, not fuse onto it.
	if err := appendLine(path, "message", map[string]int{"n": 4}); err != nil {
		t.Fatal(err)
	}
	// And one more, to push any fused line into interior position.
	if err := appendLine(path, "message", map[string]int{"n": 5}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Strict tolerant-tail decode must succeed (no interior corruption) and keep
	// every complete record: the two clean ones plus the two post-crash appends.
	// The partial record 3 is dropped; nothing else is lost.
	out, err := decodeLinesTolerantTail[map[string]int](string(raw))
	if err != nil {
		t.Fatalf("decode after torn-tail heal errored (history would be dropped): %v\nfile:\n%s", err, raw)
	}
	got := make([]int, len(out))
	for i, m := range out {
		got[i] = m["n"]
	}
	want := []int{1, 2, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("records = %v, want %v\nfile:\n%s", got, want, raw)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("records = %v, want %v", got, want)
		}
	}
}
