//go:build unix

package cron

import (
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/runs"
)

// RunStore persists per-job run history so a restart does not erase it.
// Without it @every re-arms from process start and a missed tick leaves no
// trace anywhere — the failure mode that motivated this file: an @every 3h
// job restarted mid-Saturday got 6 ticks instead of 8, and nothing anywhere
// recorded the two misses.
type RunStore interface {
	LoadStatus() (map[string]JobStatus, error)
	SaveStatus(JobStatus) error
}

// StoreRunStore is the runs-store-backed RunStore. Each job's JobStatus is
// JSON-encoded into its own row in the dedicated cron_status table (see
// runs.CronStatusSave/CronStatusLoadAll, and schemaVersion's v5 note in
// internal/runs/db.go for why this is NOT a row in the shared threads
// table: threads.session_id is load-bearing for runs.Sweep's "does this
// thread's session still exist" check, and a job name or JSON blob there
// would be deleted by the very next startup's janitor pass, before cron
// ever gets to read it back).
type StoreRunStore struct {
	Store *runs.Store
}

// LoadStatus returns every job's persisted status, keyed by name. A corrupt
// entry (JSON that fails to decode — e.g. a row written by some future,
// incompatible version) is skipped rather than failing the whole load: one
// bad row must not blank every other job's history.
func (rs StoreRunStore) LoadStatus() (map[string]JobStatus, error) {
	blobs, err := rs.Store.CronStatusLoadAll()
	if err != nil {
		return nil, fmt.Errorf("cron: load status: %w", err)
	}
	out := make(map[string]JobStatus, len(blobs))
	for name, blob := range blobs {
		var st JobStatus
		if err := json.Unmarshal([]byte(blob), &st); err != nil {
			continue
		}
		out[name] = st
	}
	return out, nil
}

// SaveStatus persists one job's status, replacing whatever was there. Called
// after every run (see Scheduler.record) — a save the caller cannot see fail
// (it only logs) must still name the job, so a persistent write failure is
// diagnosable from the app log alone.
func (rs StoreRunStore) SaveStatus(st JobStatus) error {
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("cron: encode status for %q: %w", st.Name, err)
	}
	if err := rs.Store.CronStatusSave(st.Name, string(b)); err != nil {
		return fmt.Errorf("cron: save status for %q: %w", st.Name, err)
	}
	return nil
}
