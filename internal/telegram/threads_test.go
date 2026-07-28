//go:build unix

package telegram

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestThreadIndexRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("NewThreadIndex: %v", err)
	}
	idx.Record(123, "sess-abc")

	got, ok := idx.Lookup(123)
	if !ok || got != "sess-abc" {
		t.Fatalf("Lookup(123) = %q, %v; want sess-abc, true", got, ok)
	}
}

func TestThreadIndexPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("NewThreadIndex: %v", err)
	}
	idx.Record(1, "s1")
	idx.Record(2, "s2")

	idx2, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for _, tc := range []struct {
		id   int
		want string
	}{{1, "s1"}, {2, "s2"}} {
		got, ok := idx2.Lookup(tc.id)
		if !ok || got != tc.want {
			t.Fatalf("after reopen Lookup(%d) = %q, %v; want %s, true", tc.id, got, ok, tc.want)
		}
	}
}

func TestThreadIndexUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("NewThreadIndex: %v", err)
	}
	if got, ok := idx.Lookup(999); ok {
		t.Fatalf("Lookup(999) = %q, true; want _, false", got)
	}
}

func TestThreadIndexTornFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	// A valid line followed by a torn fragment (crash mid-append).
	if err := os.WriteFile(path, []byte(`{"m":7,"s":"good"}`+"\n"+`{"m":9,`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("NewThreadIndex: %v", err)
	}
	if got, ok := idx.Lookup(7); !ok || got != "good" {
		t.Fatalf("Lookup(7) = %q, %v; want good, true", got, ok)
	}
	if _, ok := idx.Lookup(9); ok {
		t.Fatalf("Lookup(9) should be absent (torn line)")
	}
	// New records should still persist cleanly after the torn tail.
	idx.Record(11, "eleven")
	idx2, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := idx2.Lookup(11); !ok || got != "eleven" {
		t.Fatalf("after reopen Lookup(11) = %q, %v; want eleven, true", got, ok)
	}
	if got, ok := idx2.Lookup(7); !ok || got != "good" {
		t.Fatalf("after reopen Lookup(7) = %q, %v; want good, true", got, ok)
	}
}

func TestPruneThreadIndexDropsDeadKeepsLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"m":1,"s":"live-1"}`+"\n"+
			`{"m":2,"s":"dead-1"}`+"\n"+
			`{"m":3,"s":"live-2"}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	live := map[string]bool{"live-1": true, "live-2": true}
	removed, err := PruneThreadIndex(path, func(id string) bool { return live[id] })
	if err != nil {
		t.Fatalf("PruneThreadIndex: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := idx.Lookup(1); !ok || got != "live-1" {
		t.Fatalf("Lookup(1) = %q, %v; want live-1, true", got, ok)
	}
	if got, ok := idx.Lookup(3); !ok || got != "live-2" {
		t.Fatalf("Lookup(3) = %q, %v; want live-2, true", got, ok)
	}
	if _, ok := idx.Lookup(2); ok {
		t.Fatalf("Lookup(2) should be dropped (dead-1 no longer exists)")
	}
}

func TestPruneThreadIndexNoDeadIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	if err := os.WriteFile(path, []byte(`{"m":1,"s":"live-1"}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	removed, err := PruneThreadIndex(path, func(id string) bool { return true })
	if err != nil {
		t.Fatalf("PruneThreadIndex: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestPruneThreadIndexMissingFileNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.jsonl")
	removed, err := PruneThreadIndex(path, func(id string) bool { return true })
	if err != nil {
		t.Fatalf("PruneThreadIndex on missing file: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestThreadIndexConcurrentRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	idx, err := NewThreadIndex(path)
	if err != nil {
		t.Fatalf("NewThreadIndex: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Record(n, "s"+strconv.Itoa(n))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 10; i++ {
		got, ok := idx.Lookup(i)
		if !ok || got != "s"+strconv.Itoa(i) {
			t.Fatalf("Lookup(%d) = %q, %v", i, got, ok)
		}
	}
}
