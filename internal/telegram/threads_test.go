//go:build unix

package telegram

import (
	"strconv"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/runs"
)

func testStore(t *testing.T) func() *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return func() *runs.Store { return st }
}

func TestThreadIndexRoundtrip(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	idx.Record("123", "sess-abc")
	got, ok := idx.Lookup("123")
	if !ok || got != "sess-abc" {
		t.Fatalf("Lookup(123) = %q, %v; want sess-abc, true", got, ok)
	}
}

func TestThreadIndexPersistence(t *testing.T) {
	st := testStore(t)
	idx := NewThreadIndex(st, "telegram")
	idx.Record("1", "s1")
	idx.Record("2", "s2")

	// A second index over the same store (as after a restart) sees the map.
	idx2 := NewThreadIndex(st, "telegram")
	for _, tc := range []struct {
		id   string
		want string
	}{{"1", "s1"}, {"2", "s2"}} {
		got, ok := idx2.Lookup(tc.id)
		if !ok || got != tc.want {
			t.Fatalf("Lookup(%s) = %q, %v; want %s, true", tc.id, got, ok, tc.want)
		}
	}
}

func TestThreadIndexUnknown(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	if got, ok := idx.Lookup("999"); ok {
		t.Fatalf("Lookup(999) = %q, true; want _, false", got)
	}
}

// Two front-end surfaces over one store never cross-resolve each other's ids.
func TestThreadIndexSurfaceIsolation(t *testing.T) {
	st := testStore(t)
	tg := NewThreadIndex(st, "telegram")
	sv := NewThreadIndex(st, "serve")
	tg.Record("7", "tg-sess")
	sv.Record("7", "serve-sess")
	if got, _ := tg.Lookup("7"); got != "tg-sess" {
		t.Fatalf("telegram Lookup(7) = %q", got)
	}
	if got, _ := sv.Lookup("7"); got != "serve-sess" {
		t.Fatalf("serve Lookup(7) = %q", got)
	}
}

func TestThreadIndexConcurrentRecord(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Record(strconv.Itoa(n), "s"+strconv.Itoa(n))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 10; i++ {
		got, ok := idx.Lookup(strconv.Itoa(i))
		if !ok || got != "s"+strconv.Itoa(i) {
			t.Fatalf("Lookup(%d) = %q, %v", i, got, ok)
		}
	}
}
