//go:build unix

package cron

import (
	"fmt"
	"sync"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Dispatcher is the subset of *shell3.Session the scheduler needs (faked in tests).
type Dispatcher interface {
	Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error)
}

// JobStatus is a job plus its most recent run, for the Cron view.
type JobStatus struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Prompt    string `json:"prompt,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
	Report    string `json:"report"`
	LastRun   string `json:"last_run,omitempty"` // RFC3339, "" if never
	LastSubID string `json:"last_sub_id,omitempty"`
	// LastOK/LastErr/LastMillis describe the RUN, not its dispatch: the fire
	// path only knows the subagent was accepted, so the outcome arrives later
	// from the completion router (RecordOutcome). Between a fire and its
	// outcome these still hold the PREVIOUS run's — deliberately, since the
	// alternative is inventing a verdict for work still in flight.
	LastOK     bool   `json:"last_ok"`
	LastErr    string `json:"last_err,omitempty"`
	LastMillis int64  `json:"last_millis,omitempty"`
	Runs       int    `json:"runs,omitempty"`
	Failures   int    `json:"failures,omitempty"`
	// OutcomeRecorded marks that LastSubID's run has already reported. Cleared
	// at each fire; see RecordOutcome for why at-least-once delivery makes it
	// load-bearing rather than bookkeeping about bookkeeping.
	OutcomeRecorded bool `json:"outcome_recorded,omitempty"`
}

// Scheduler arms one robfig/cron entry per job and dispatches on tick.
type Scheduler struct {
	disp     Dispatcher
	store    RunStore // nil = in-memory only (tests, library use); see NewWithStore
	log      applog.Logger
	c        *robcron.Cron
	mu       sync.Mutex
	wg       sync.WaitGroup // scheduled and manual dispatch calls admitted below
	stopping bool           // closes fire admission before Stop waits
	jobs     []shell3.CronJob
	last     map[string]JobStatus // by job name
	now      func() time.Time     // injectable clock for tests
}

// New validates every schedule and arms an entry per job, failing fast on a
// malformed one. Every job is an agent dispatch whose result routes as mail
// through the job runtime, which is also what reports the run's outcome back
// here (RecordOutcome).
//
// New is NewWithStore with a nil store: in-memory only, no run history.
func New(disp Dispatcher, jobs []shell3.CronJob) (*Scheduler, error) {
	return NewWithStore(disp, nil, jobs)
}

// NewWithStore is New plus a RunStore, restoring each job's counters and last
// run from it. A job the store has never seen — or a nil store — starts from a
// zero JobStatus, so a test never needs a runs store to build a scheduler.
func NewWithStore(disp Dispatcher, store RunStore, jobs []shell3.CronJob) (*Scheduler, error) {
	s := &Scheduler{
		disp:  disp,
		store: store,
		log:   applog.Noop{},
		c:     robcron.New(),
		jobs:  jobs,
		last:  map[string]JobStatus{},
		now:   time.Now,
	}
	var restored map[string]JobStatus
	if store != nil {
		// A load failure must not block startup: cron arms without the
		// restored counts, and the next run re-establishes history.
		if r, err := store.LoadStatus(); err == nil {
			restored = r
		}
	}
	for _, j := range jobs {
		job := j // capture
		st := JobStatus{Name: job.Name, Schedule: job.Schedule, Agent: job.Agent, Prompt: job.Prompt, WorkDir: job.WorkDir, Report: job.Report.String()}
		if r, ok := restored[job.Name]; ok {
			st.LastRun = r.LastRun
			st.LastSubID = r.LastSubID
			st.LastOK = r.LastOK
			st.LastErr = r.LastErr
			st.LastMillis = r.LastMillis
			st.Runs = r.Runs
			st.Failures = r.Failures
			st.OutcomeRecorded = r.OutcomeRecorded
		}
		s.last[job.Name] = st
		// Overlapping fires are allowed: each tick is a fresh subagent.
		if _, err := s.c.AddFunc(job.Schedule, func() { s.fire(job) }); err != nil {
			return nil, fmt.Errorf("cron: job %q bad schedule %q: %w", job.Name, job.Schedule, err)
		}
	}
	return s, nil
}

// SetLogger installs where a failed SaveStatus is reported — never fatal, but
// silent bookkeeping loss must still land somewhere findable. Guarded by s.mu
// a /reload rewires it while robcron may be ticking.
func (s *Scheduler) SetLogger(l applog.Logger) {
	if l == nil {
		l = applog.Noop{}
	}
	s.mu.Lock()
	s.log = l
	s.mu.Unlock()
}

// beginFire admits one dispatch. Stop closes admission under the same mutex
// before waiting, so no positive WaitGroup.Add can race a zero-count Wait.
func (s *Scheduler) beginFire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.wg.Add(1)
	return true
}

// fire dispatches one job to its agent and records that the run started. The
// run's real result arrives later, at RecordOutcome. Direct callers run it
// synchronously; robfig invokes it on its own worker goroutine.
func (s *Scheduler) fire(j shell3.CronJob) {
	if !s.beginFire() {
		return
	}
	defer s.wg.Done()
	s.fireAdmitted(j)
}

func (s *Scheduler) fireAdmitted(j shell3.CronJob) {
	opts := shell3.DispatchOpts{
		WorkDir: j.WorkDir, Description: "cron:" + j.Name,
		CronJob: j.Name, Report: j.Report,
		// The prompt rides along as context: the agent judges a result far
		// better knowing what the job was created to do.
		Note: "this is the cron job's standing prompt: " + j.Prompt,
	}
	id, err := s.disp.Dispatch(j.Agent, j.Prompt, opts)
	s.recordDispatch(j, id, err)
}

// recordDispatch records that a run STARTED, and persists it: the outcome
// arrives later and matches on LastSubID, so a fire whose write did not
// survive a crash would have nothing to match against.
//
// A dispatch REJECTION is the one failure counted here — no completion will
// ever arrive for it — and it marks the outcome recorded so a stale report
// from an earlier run cannot overwrite it.
func (s *Scheduler) recordDispatch(j shell3.CronJob, subID string, err error) {
	s.mu.Lock()
	st := s.last[j.Name]
	st.LastRun = s.now().UTC().Format(time.RFC3339)
	st.Runs++
	if err != nil {
		st.LastSubID = ""
		st.OutcomeRecorded = true
		st.LastOK = false
		st.LastErr = err.Error()
		st.LastMillis = 0
		st.Failures++
	} else {
		st.LastSubID = subID
		st.OutcomeRecorded = false
	}
	s.last[j.Name] = st
	s.persist(j.Name, st)
}

// RecordOutcome records what a dispatched run actually DID, reported by the
// completion router (shell3.Runtime's cron-outcome hook) once the subagent's
// turn ends — clean, failed, killed, or lost to a restart.
//
// Two guards, both load-bearing. Completion delivery is at-least-once: a post
// the transport rejected is re-dispatched every redeliverEvery until it lands,
// and a leftover outbox row replays at the next boot, so the SAME run reaches
// here repeatedly — counting each pass would inflate exactly the number this
// exists to make honest. And an outcome whose sub id is not the current run's
// is a straggler from a fire a later one has superseded; the table keeps the
// latest run only, so it is dropped rather than backdating the row.
//
// A job this scheduler does not declare is dropped too: never invent a row for
// a name a /reload removed.
//
// Accepted race: a run that finished before its own fire took s.mu records
// against the previous LastSubID and is then re-armed by recordDispatch, so a
// later redelivery could count it twice. The window is one mutex write against
// a whole LLM turn; restructuring for it costs more than it saves.
func (s *Scheduler) RecordOutcome(o shell3.CronOutcome) {
	s.mu.Lock()
	st, known := s.last[o.Job]
	if !known || st.OutcomeRecorded || (o.SubID != "" && st.LastSubID != "" && o.SubID != st.LastSubID) {
		s.mu.Unlock()
		return
	}
	st.OutcomeRecorded = true
	st.LastOK = o.OK
	st.LastErr = o.ErrText
	st.LastMillis = o.Elapsed.Milliseconds()
	if !o.OK {
		st.Failures++
	}
	s.last[o.Job] = st
	s.persist(o.Job, st)
}

// persist writes one job's status through the store, releasing s.mu first: a
// stuck store write must not block the next tick. Call with s.mu HELD; it
// unlocks. A run this does not save is invisible after the next restart, but
// the failure is only ever logged — bookkeeping must not break a schedule.
func (s *Scheduler) persist(name string, st JobStatus) {
	store, log := s.store, s.log
	s.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.SaveStatus(st); err != nil {
		if log != nil {
			log.Warn("cron: save run status failed", "job", name, "error", err)
		}
	}
}

// Start begins firing on schedule. Stop blocks until scheduled and manual
// dispatch calls return; in-flight subagents are joined by Runtime.Close.
func (s *Scheduler) Start() { s.c.Start() }
func (s *Scheduler) Stop() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.c.Stop()
	s.wg.Wait()
}

// Run fires a job by name, erroring immediately on an unknown one and
// returning before a known one completes. A scheduled tick already has its own
// goroutine, but /run is called from the bot's single update loop, and even a
// dispatch can block on session setup — firing synchronously would freeze that
// loop, /stop included, for the duration.
func (s *Scheduler) Run(name string) error {
	for _, j := range s.jobs {
		if j.Name == name {
			if !s.beginFire() {
				return fmt.Errorf("cron scheduler is stopped")
			}
			job := j
			go func() {
				defer s.wg.Done()
				s.fireAdmitted(job)
			}()
			return nil
		}
	}
	return fmt.Errorf("no job named %q", name)
}

// Jobs returns each configured job with its last run, for the Cron view.
func (s *Scheduler) Jobs() []JobStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, s.last[j.Name])
	}
	return out
}
