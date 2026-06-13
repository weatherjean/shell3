// Package jobstore adapts the canonical *store.Store to the bgjobs.Registry
// interface, translating between bgjobs.Job and store.Job.
package jobstore

import (
	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/store"
)

// Store adapts *store.Store to bgjobs.Registry, translating bgjobs.Job <-> store.Job.
type Store struct{ st *store.Store }

// New wraps st as a bgjobs.Registry.
func New(st *store.Store) *Store { return &Store{st: st} }

func (a *Store) Add(j bgjobs.Job) error {
	return a.st.AddJob(store.Job{
		ID: j.ID, PID: j.PID, Cmd: j.Cmd, Log: j.Log, Workdir: j.Workdir, StartedAt: j.StartedAt,
	})
}

func (a *Store) List(workdir string) ([]bgjobs.Job, error) {
	rows, err := a.st.ListJobs(workdir, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]bgjobs.Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, bgjobs.Job{
			ID: r.ID, PID: r.PID, Cmd: r.Cmd, Log: r.Log, Workdir: r.Workdir, StartedAt: r.StartedAt,
		})
	}
	return out, nil
}

func (a *Store) Clear(workdir string) (int, error) { return a.st.ClearJobs(workdir) }
