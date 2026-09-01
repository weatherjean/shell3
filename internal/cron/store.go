//go:build unix

package cron

import (
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/runs"
)

// RunStore persists per-job status across restarts.
type RunStore interface {
	LoadStatus() (map[string]JobStatus, error)
	SaveStatus(JobStatus) error
}

// StoreRunStore stores each JobStatus as JSON in cron_status.
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
