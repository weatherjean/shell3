//go:build unix

package cron

import (
	"fmt"
	"sync"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// Dispatcher is the subset of *shell3.Session the scheduler needs (faked in tests).
type Dispatcher interface {
	Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error)
}

// JobStatus is a job plus its most recent run, for the dashboard.
type JobStatus struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Notify    bool   `json:"notify"`
	LastRun   string `json:"last_run,omitempty"` // RFC3339, "" if never
	LastSubID string `json:"last_sub_id,omitempty"`
}

// Scheduler arms one robfig/cron entry per job and dispatches on tick.
type Scheduler struct {
	disp Dispatcher
	c    *robcron.Cron
	mu   sync.Mutex
	jobs []shell3.CronJob
	last map[string]JobStatus // by job name
	now  func() time.Time     // injectable clock for tests
}

// New validates every schedule and arms an entry per job. Returns an error if
// any schedule is malformed (fail-fast at startup).
func New(disp Dispatcher, jobs []shell3.CronJob) (*Scheduler, error) {
	s := &Scheduler{
		disp: disp,
		c:    robcron.New(),
		jobs: jobs,
		last: map[string]JobStatus{},
		now:  time.Now,
	}
	for _, j := range jobs {
		job := j // capture
		s.last[job.Name] = JobStatus{Name: job.Name, Schedule: job.Schedule, Agent: job.Agent, Notify: job.Notify}
		// v1 deliberately allows overlapping fires: each tick is a fresh
		// subagent (plain AddFunc, no cron.SkipIfStillRunning wrapper). If a
		// job's runs prove noisy, wrap with a skip-if-running chain later.
		if _, err := s.c.AddFunc(job.Schedule, func() { s.fire(job) }); err != nil {
			return nil, fmt.Errorf("cron: job %q bad schedule %q: %w", job.Name, job.Schedule, err)
		}
	}
	return s, nil
}

// fire dispatches one job and records its run status.
func (s *Scheduler) fire(j shell3.CronJob) {
	id, err := s.disp.Dispatch(j.Agent, j.Prompt, shell3.DispatchOpts{
		WorkDir: j.WorkDir, Label: "cron:" + j.Name, Notify: j.Notify,
	})
	s.mu.Lock()
	st := s.last[j.Name]
	st.LastRun = s.now().UTC().Format(time.RFC3339)
	if err == nil {
		st.LastSubID = id
	}
	s.last[j.Name] = st
	s.mu.Unlock()
}

// Start begins firing on schedule. Stop halts it (blocks until running jobs'
// dispatch calls return; in-flight subagents are joined by Runtime.Close).
func (s *Scheduler) Start() { s.c.Start() }
func (s *Scheduler) Stop()  { s.c.Stop() }

// Run fires a job by name immediately. Returns an error if the name is unknown.
func (s *Scheduler) Run(name string) error {
	for _, j := range s.jobs {
		if j.Name == name {
			s.fire(j)
			return nil
		}
	}
	return fmt.Errorf("no job named %q", name)
}

// Jobs returns each configured job with its last run, for the dashboard.
func (s *Scheduler) Jobs() []JobStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, s.last[j.Name])
	}
	return out
}
