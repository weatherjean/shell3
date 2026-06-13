package store_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

// TestCanonicalDB_ConcurrentWritersNoLoss stress-tests the single-canonical-DB
// model: many INDEPENDENT connections (each its own *sql.DB pool, standing in
// for separate OS processes — parent, N subagents, revivers, the bot) hammer the
// SAME file with the writes that orchestration depends on (AppendInbox +
// SetLiveness). Per-project DBs barely contended cross-process; one shared file
// concentrates all writes, so this proves WAL + busy_timeout(5000) serialize
// them without dropping any inbox row — the exact write whose silent loss caused
// the original "never pinged back" bug.
func TestCanonicalDB_ConcurrentWritersNoLoss(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shell3.db")

	seed, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed: %v", err)
	}
	sid, err := seed.StartSession("proj", "/w")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	const (
		writers           = 16
		appendsPerWriter  = 64
		livenessPerWriter = 16
	)

	var wg sync.WaitGroup
	errCh := make(chan error, writers*(appendsPerWriter+livenessPerWriter))
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each writer opens its OWN store handle = its own connection pool,
			// the cross-process contention the single DB now concentrates.
			st, err := store.Open(dbPath)
			if err != nil {
				errCh <- fmt.Errorf("writer %d open: %w", w, err)
				return
			}
			defer func() { _ = st.Close() }()
			for i := 0; i < appendsPerWriter; i++ {
				if err := st.AppendInbox(sid, []byte(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i))); err != nil {
					errCh <- fmt.Errorf("writer %d append %d: %w", w, i, err)
				}
				if i < livenessPerWriter {
					if err := st.SetLiveness(sid, w*1000+i, "/tmp/x.sock", "live"); err != nil {
						errCh <- fmt.Errorf("writer %d liveness %d: %w", w, i, err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)

	var firstErr error
	n := 0
	for err := range errCh {
		n++
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		t.Fatalf("%d concurrent write(s) failed under contention (busy_timeout insufficient?); first: %v", n, firstErr)
	}

	// Every inbox row must have landed — no silent drops.
	reader, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer func() { _ = reader.Close() }()
	var got int
	if err := reader.DB().QueryRow(`SELECT COUNT(*) FROM inbox WHERE session_id = ?`, sid).Scan(&got); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if want := writers * appendsPerWriter; got != want {
		t.Fatalf("inbox rows = %d, want %d (lost %d writes)", got, want, want-got)
	}
}
