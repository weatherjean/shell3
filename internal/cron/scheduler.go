//go:build unix

package cron

import (
	"context"
	"fmt"
	"sync"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Dispatcher is the subset of *shell3.Session the scheduler needs (faked in tests).
type Dispatcher interface {
	Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error)
}

// ToolRunner runs one kit tool by name in the given working directory. The
// scheduler holds this rather than a kit.Runner so a tool job is testable
// without a shell.
type ToolRunner interface {
	RunTool(ctx context.Context, name, workDir string, args map[string]any) (string, error)
}

// toolJobTimeout bounds a tool job. It matches the foreground bash cap: a
// tool job blocks its scheduler slot exactly the way a foreground call
// blocks a turn.
const toolJobTimeout = 120 * time.Second

// JobStatus is a job plus its most recent run, for the Cron view.
type JobStatus struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Tool      string `json:"tool,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	WorkDir   string `json:"work_dir,omitempty"`
	Direct    bool   `json:"direct"`
	LastRun   string `json:"last_run,omitempty"` // RFC3339, "" if never
	LastSubID string `json:"last_sub_id,omitempty"`
	// LastOK/LastErr/LastMillis/Runs/Failures mean DIFFERENT things depending
	// on the job kind, because the two fire paths learn different things:
	//
	//   - Tool job: these describe the actual run. err comes straight from
	//     ToolRunner.RunTool, so LastOK/LastErr/Failures reflect whether the
	//     tool itself succeeded.
	//   - Agent job: err comes from Dispatcher.Dispatch, which reports only
	//     whether the subagent was ACCEPTED for dispatch — not whether its
	//     run succeeded or failed. An agent job that dispatches cleanly every
	//     night but fails its actual work every night will show
	//     LastOK=true, Failures=0 forever; the real outcome is only ever
	//     visible in the mail/⚠️-floor post the job runtime sends
	//     separately, not here. Feeding real agent outcomes back into these
	//     fields is future work (tracked for the RunStore-based reporting
	//     task), not something this scheduler observes today.
	LastOK     bool   `json:"last_ok"`
	LastErr    string `json:"last_err,omitempty"`
	LastMillis int64  `json:"last_millis,omitempty"`
	Runs       int    `json:"runs,omitempty"`
	Failures   int    `json:"failures,omitempty"`
}

// Scheduler arms one robfig/cron entry per job and dispatches on tick.
type Scheduler struct {
	disp  Dispatcher
	tools ToolRunner
	store RunStore // nil = in-memory only (tests, library use); see NewWithStore
	log   applog.Logger
	c     *robcron.Cron
	mu    sync.Mutex
	jobs  []shell3.CronJob
	last  map[string]JobStatus // by job name
	now   func() time.Time     // injectable clock for tests
	// post, when set, delivers a tool job's own result or failure straight to
	// the user — a tool job spends no model turn, so nothing else ever
	// speaks for it. It takes a shell3.CompletionPost (not a bare string) so
	// the host renders exactly one marker/prefix; fireTool sets CronJob only
	// on a SUCCESS post (Text stays bare, letting PostCompletion's
	// CronJob-prefix branch supply "⏰ job: ") and leaves it unset on a
	// FAILURE post (Text already carries its own "⚠️ job failed: …" marker —
	// setting CronJob there would hit PostCompletion's CronJob branch before
	// its failure branch and double-wrap it: "⏰ job: ⚠️ job failed: …"). See
	// fireTool for the exact reasoning on each branch. nil-safe: unset in
	// tests, where nothing should post.
	post func(shell3.CompletionPost)
}

// New validates every schedule and arms an entry per job. Returns an error if
// any schedule is malformed (fail-fast at startup). Completion delivery for
// an agent job is entirely the job runtime's: each fire is a Dispatch whose
// result routes as mail to the main agent (or, with direct: true, as a raw
// post to the user). A tool job has no dispatch and no model turn to route
// through, so it posts for itself via SetPost.
//
// New has no run history to restore from — it is NewWithStore with a nil
// store, which means in-memory only (tests, and any caller that doesn't want
// restart-durable history).
func New(disp Dispatcher, tools ToolRunner, jobs []shell3.CronJob) (*Scheduler, error) {
	return NewWithStore(disp, tools, nil, jobs)
}

// NewWithStore is New plus a RunStore: on construction, each job's Runs,
// Failures, LastRun, LastOK, LastErr, LastMillis and LastSubID are restored
// from store (falling back to a fresh zero JobStatus for a job store has
// never seen, or when store is nil). A nil store degrades to New's
// in-memory-only behaviour rather than panicking, so a library caller or a
// test never needs to stand up a runs store just to build a scheduler.
func NewWithStore(disp Dispatcher, tools ToolRunner, store RunStore, jobs []shell3.CronJob) (*Scheduler, error) {
	s := &Scheduler{
		disp:  disp,
		tools: tools,
		store: store,
		log:   applog.Noop{},
		c:     robcron.New(),
		jobs:  jobs,
		last:  map[string]JobStatus{},
		now:   time.Now,
	}
	var restored map[string]JobStatus
	if store != nil {
		// A load failure (corrupt store, closed database) must not block
		// startup: cron still arms, just without restored counts — the next
		// run re-establishes history from there.
		if r, err := store.LoadStatus(); err == nil {
			restored = r
		}
	}
	for _, j := range jobs {
		job := j // capture
		st := JobStatus{Name: job.Name, Schedule: job.Schedule, Agent: job.Agent, Tool: job.Tool, Prompt: job.Prompt, WorkDir: job.WorkDir, Direct: job.Direct}
		if r, ok := restored[job.Name]; ok {
			st.LastRun = r.LastRun
			st.LastSubID = r.LastSubID
			st.LastOK = r.LastOK
			st.LastErr = r.LastErr
			st.LastMillis = r.LastMillis
			st.Runs = r.Runs
			st.Failures = r.Failures
		}
		s.last[job.Name] = st
		// Overlapping fires are allowed: each tick is a fresh subagent (no
		// SkipIfStillRunning wrapper).
		if _, err := s.c.AddFunc(job.Schedule, func() { s.fire(job) }); err != nil {
			return nil, fmt.Errorf("cron: job %q bad schedule %q: %w", job.Name, job.Schedule, err)
		}
	}
	return s, nil
}

// SetLogger installs where a failed SaveStatus is reported — never fatal to a
// scheduled run, but silent bookkeeping loss is its own kind of bug, so it
// must land somewhere the operator can find it. Unset defaults to a Noop
// (tests, library use). Guarded by s.mu like SetPost: a /reload may rewire
// this while robcron is still ticking on its own goroutine.
func (s *Scheduler) SetLogger(l applog.Logger) {
	if l == nil {
		l = applog.Noop{}
	}
	s.mu.Lock()
	s.log = l
	s.mu.Unlock()
}

// SetPost installs the callback a tool job uses to post its own result or
// failure. Unset (nil) means "no delivery surface" (library use, tests) —
// fireTool then just records the outcome silently rather than panicking.
// Guarded by s.mu: a /reload rewires post on a scheduler that may still be
// ticking on a robcron goroutine, so an unsynchronized field would race.
func (s *Scheduler) SetPost(fn func(shell3.CompletionPost)) {
	s.mu.Lock()
	s.post = fn
	s.mu.Unlock()
}

// postFn reads the current post callback under lock — see SetPost.
func (s *Scheduler) postFn() func(shell3.CompletionPost) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.post
}

// fire runs one job — a tool call with no model in the loop, or the existing
// agent dispatch — and records its outcome.
func (s *Scheduler) fire(j shell3.CronJob) {
	if j.Tool != "" {
		s.fireTool(j)
		return
	}
	s.fireAgent(j)
}

// fireAgent dispatches one job to an agent and records its run status.
func (s *Scheduler) fireAgent(j shell3.CronJob) {
	start := s.now()
	opts := shell3.DispatchOpts{
		WorkDir: j.WorkDir, Description: "cron:" + j.Name,
		CronJob: j.Name, Direct: j.Direct,
		// The job prompt rides along as context: the agent judges a
		// result far better knowing what the job was created to do.
		Note: "this is the cron job's standing prompt: " + j.Prompt,
	}
	id, err := s.disp.Dispatch(j.Agent, j.Prompt, opts)
	if err == nil {
		s.mu.Lock()
		st := s.last[j.Name]
		st.LastSubID = id
		s.last[j.Name] = st
		s.mu.Unlock()
	}
	s.record(j, err, s.now().Sub(start))
}

// fireTool runs a tool job with no model in the loop. The result reaches the
// user only when it says something: a tool that prints nothing (or the
// NO_REPLY sentinel) on an idempotent no-op stays silent, which is the point
// of scheduling it every 30 minutes.
func (s *Scheduler) fireTool(j shell3.CronJob) {
	start := s.now()
	if s.tools == nil {
		// A scheduler wired without a ToolRunner (a misconfigured host) must
		// fail closed with a reason, not panic on the first tick.
		s.record(j, fmt.Errorf("cron: job %q names tool %q but no tool runner is wired", j.Name, j.Tool), s.now().Sub(start))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolJobTimeout)
	defer cancel()
	out, err := s.tools.RunTool(ctx, j.Tool, j.WorkDir, nil)
	s.record(j, err, s.now().Sub(start))
	switch {
	case err != nil:
		if post := s.postFn(); post != nil {
			// CronJob is deliberately left UNSET here. PostCompletion
			// (internal/telegram/bot.go) tests CronJob != "" before it
			// tests "is this already a failure" — a set CronJob would hit
			// that branch first and wrap this already-self-describing
			// "⚠️ job failed: …" text in a SECOND "⏰ job: " prefix
			// (⏰ sync: ⚠️ sync failed: …). Leaving CronJob empty routes it
			// through PostCompletion's failure branch instead, which adds
			// no prefix of its own — matching shell3.floorText's
			// convention for a real cron dispatch's failure floor.
			post(shell3.CompletionPost{
				Text: fmt.Sprintf("⚠️ %s failed: %s", j.Name, strutil.Truncate(err.Error(), 500))})
		}
	case !strutil.IsNoReply(out):
		if post := s.postFn(); post != nil {
			// CronJob IS set here: a success has no ⚠️ marker of its own, so
			// PostCompletion's CronJob branch is what supplies the single
			// "⏰ job: " prefix — Text stays bare (see the Scheduler.post
			// field doc) so it isn't doubled.
			post(shell3.CompletionPost{CronJob: j.Name, Text: strutil.Truncate(out, 2000)})
		}
	}
}

// record updates the outcome fields shared by both fire paths — LastRun plus
// the latest result and running totals — under one lock, so the dash's cron
// views see a tool job's last-run time exactly like an agent job's. It then
// persists the new status (see NewWithStore/RunStore): a run this doesn't
// save is invisible after the next restart, which is the whole reason
// RunStore exists.
func (s *Scheduler) record(j shell3.CronJob, err error, elapsed time.Duration) {
	s.mu.Lock()
	st := s.last[j.Name]
	st.LastRun = s.now().UTC().Format(time.RFC3339)
	st.LastMillis = elapsed.Milliseconds()
	st.Runs++
	if err != nil {
		st.LastOK = false
		st.LastErr = err.Error()
		st.Failures++
	} else {
		st.LastOK = true
		st.LastErr = ""
	}
	s.last[j.Name] = st
	store, log := s.store, s.log
	s.mu.Unlock()

	if store == nil {
		return
	}
	// Saved outside the lock: a slow or stuck store write must not block the
	// next tick's fire from updating s.last.
	if err := store.SaveStatus(st); err != nil {
		if log != nil {
			log.Warn("cron: save run status failed", "job", j.Name, "error", err)
		}
	}
}

// Start begins firing on schedule. Stop halts it (blocks until running jobs'
// dispatch calls return; in-flight subagents are joined by Runtime.Close).
func (s *Scheduler) Start() { s.c.Start() }
func (s *Scheduler) Stop()  { s.c.Stop() }

// Run fires a job by name. Returns an error immediately for an unknown name;
// a known job fires on its own goroutine and Run returns before it
// completes. A scheduled tick already gets its own goroutine (robcron calls
// each entry's function independently) — but /run (the manual-trigger
// command) is the one caller that runs on a single serialized loop (the
// bot's update loop), and a tool job can block for up to toolJobTimeout.
// Firing synchronously here would freeze that loop — not just the next
// message, but /stop too — for the exact duration a tool job's
// whole reason for existing was to avoid spending on a model turn.
func (s *Scheduler) Run(name string) error {
	for _, j := range s.jobs {
		if j.Name == name {
			job := j
			go s.fire(job)
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
