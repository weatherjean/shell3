package shell3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/strutil"
)

// JobKind discriminates an in-process background job's payload.
type JobKind int

const (
	JobCommand  JobKind = iota // a shell command (bash_bg)
	JobSubagent                // a child Session (task tool)
)

// String returns "command"/"subagent" for logs and diagnostics.
func (k JobKind) String() string {
	switch k {
	case JobCommand:
		return "command"
	case JobSubagent:
		return "subagent"
	}
	return fmt.Sprintf("JobKind(%d)", int(k))
}

const defaultMaxConcurrent = 8

// bgWaitDelay bounds cmd.Wait on the stdio pipes after a cancel. Longer than
// chat's bashWaitDelay — background jobs are off the turn's critical path.
const bgWaitDelay = 3 * time.Second

// maxDoneJobs caps how many finished jobs are retained in memory.
const maxDoneJobs = 100

// ringBuffer keeps the last maxBytes of output for a command job.
type ringBuffer struct {
	mu      sync.Mutex
	buf     []byte
	maxSize int
}

func newRingBuffer(maxSize int) *ringBuffer { return &ringBuffer{maxSize: maxSize} }

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.maxSize {
		r.buf = r.buf[len(r.buf)-r.maxSize:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// jobLogMaxBytes caps a command job's on-disk log: enough for a full build or
// fetch result, not enough for a runaway job to fill the disk. The in-memory
// ring keeps the TAIL, the log the HEAD.
const jobLogMaxBytes = 1 << 20 // 1 MiB

// cappedFileWriter appends until the cap, then swallows writes, recording
// that it truncated. A write error silently disables it — the log is
// best-effort, the ring buffer stays authoritative.
type cappedFileWriter struct {
	mu      sync.Mutex
	f       *os.File
	written int64
	dead    bool
}

func newCappedFileWriter(path string) *cappedFileWriter {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil
	}
	return &cappedFileWriter{f: f}
}

func (w *cappedFileWriter) Write(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead || w.written >= jobLogMaxBytes {
		return
	}
	if rem := jobLogMaxBytes - w.written; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := w.f.Write(p)
	w.written += int64(n)
	if err != nil {
		w.dead = true
	}
	if w.written >= jobLogMaxBytes {
		_, _ = w.f.WriteString("\n…(log capped)\n")
	}
}

func (w *cappedFileWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dead = true
	_ = w.f.Close()
}

// jobSink tees each write to a ring buffer and to an emit callback, so both
// the Jobs view and the JobEvents bus see live output. It is cmd.Stdout for
// command jobs and the event sink for subagents; a non-nil file also persists
// the capped output to the job's log.
type jobSink struct {
	ring *ringBuffer
	emit func(chunk string)
	file *cappedFileWriter
}

func (s *jobSink) Write(p []byte) (int, error) {
	n, err := s.ring.Write(p)
	if s.file != nil {
		s.file.Write(p)
	}
	s.emit(string(p))
	return n, err
}

// String is the accumulated ring-buffer content, the interface bgJob.out
// callers rely on.
func (s *jobSink) String() string { return s.ring.String() }

type bgJob struct {
	id       string
	kind     JobKind
	title    string // command text or subagent description
	agent    string // subagent jobs: the spawned agent's name ("" for commands)
	parent   *Session
	parentID string
	// parentSession is the parent's RUNS session id, the directory a job log
	// lives under — not parentID, the in-process handle ("s1"). A front-end
	// linking to the log needs this one; they are not interchangeable.
	parentSession string
	startedAt     time.Time
	cancel        context.CancelFunc
	out           *jobSink // live output: command stdout/stderr, or subagent event stream
	childID       string   // subagent: child runs id (transcript source)
	// report is the single axis for what this job's finish does to the chat:
	// raw output, a report turn the agent may answer, or one it must.
	report   notify.ReportMode
	detached bool
	// note is the spawner's intent hint, carried into the completion mail.
	note string
	// cronJob names the cron job for a cron dispatch, "" otherwise. Routes
	// the ⏰ prefix and the ownerless wake path.
	cronJob string

	// Subagent keep-open lifecycle (guarded by jobManager.mu). A subagent
	// ending its main turn with bash_bg jobs still running reports done, but
	// its child session LINGERS so each later completion can resume it for a
	// follow-up turn whose summary reaches the root as an agent_update.
	child       *Session // subagent: the child session handle (nil for commands)
	childClosed bool     // child.Close() has run; follow-ups degrade to raw notices
	statusPolls int      // task_status checks while running — repeats get told to stop polling
	lingering   bool     // main turn ended, child kept open for live bg jobs
	driver      bool     // a follow-up driver goroutine is active for this job
	followUps   int      // follow-up turns run so far (capped at maxFollowUps)
	noFollowUps bool     // poisoned (cancelled/failed): no further follow-up turns

	// logPath is runs/<parent-session>/jobs/<id>.log, "" when no store or
	// parent existed at start. The completion mail and task_status point here.
	logPath string

	// suppress marks a job killed by superstop: dispatchCompletion drops its
	// event, so the one summary is the record rather than N ⚠️ posts.
	suppress bool

	// shutdownCancel marks a job cancelAll killed on teardown: its "context
	// canceled" failure was manufactured by the shutdown, so its outbox event
	// is dropped and the RUNNING marker survives instead — the next boot then
	// reports it honestly as "was running when shell3 stopped".
	shutdownCancel bool

	// markerID is the job's outbox "running" row (0 = none), deleted when the
	// job finishes unless shutdownCancel left it for boot-time recovery.
	markerID int64

	// set on completion; read under jobManager.mu
	finished bool
	exit     *int      // command jobs: exit code (nil while running)
	summary  string    // subagent jobs: completion summary
	errText  string    // subagent jobs: last turn error ("" = clean run)
	endedAt  time.Time // zero while running
}

type jobManager struct {
	mu   sync.Mutex
	wg   sync.WaitGroup // tracks live job goroutines for Close ordering
	rt   *Runtime
	jobs map[string]*bgJob
	max  int
	seq  int

	// closing is set by cancelAll: no new follow-up drivers start and
	// in-flight completions degrade. driverCtx bounds every follow-up turn so
	// Close can abort them promptly.
	closing      bool
	driverCtx    context.Context
	driverCancel context.CancelFunc

	// undelivered holds outbox rows whose user-facing post failed to send:
	// they were kept rather than deleted, and RedeliverUndelivered retries
	// them. Boot-time recovery covers the same rows if the process dies
	// first — this set exists so a LIVE process retries without a restart.
	undelivered map[int64]struct{}
}

func newJobManager(rt *Runtime, maxConcurrent int) *jobManager {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &jobManager{
		rt: rt, jobs: map[string]*bgJob{},
		max:       maxConcurrent,
		driverCtx: ctx, driverCancel: cancel,
	}
}

// maxFollowUps caps a subagent's follow-up turns. A follow-up can start
// another bash_bg job, so without a cap the loop chains unattended LLM turns
// forever. Past it, completions degrade to raw bg_done notices at the root.
const maxFollowUps = 5

// nextID must be called under m.mu.
func (m *jobManager) nextID(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s%d", prefix, m.seq)
}

// capError is the shared cap-exceeded error for both job kinds.
func (m *jobManager) capError() error {
	return fmt.Errorf("background-job cap %d reached; wait for a job to finish", m.max)
}

// runningCount counts non-finished jobs. Under m.mu.
func (m *jobManager) runningCount() int {
	n := 0
	for _, j := range m.jobs {
		if !j.finished {
			n++
		}
	}
	return n
}

// evictOldestDoneIfNeeded drops the oldest finished job past maxDoneJobs.
// Under m.mu.
func (m *jobManager) evictOldestDoneIfNeeded() {
	var (
		oldest    *bgJob
		doneCount int
	)
	for _, j := range m.jobs {
		if !j.finished {
			continue
		}
		// A lingering subagent is not evictable: finishCommand resolves
		// job→subagent ownership through this map, and dropping the entry
		// would orphan the child's remaining completions.
		if j.kind == JobSubagent && j.child != nil && !j.childClosed {
			continue
		}
		doneCount++
		if oldest == nil || j.endedAt.Before(oldest.endedAt) {
			oldest = j
		}
	}
	if doneCount > maxDoneJobs && oldest != nil {
		delete(m.jobs, oldest.id)
	}
}

// startCommand launches argv as a managed background job. env appends "K=V"
// entries to the inherited environment; nil inherits it unchanged. report is
// what the finish does to the chat; note is carried into the completion mail.
func (m *jobManager) startCommand(parent *Session, command, workdir string, argv, env []string, report notify.ReportMode, note string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty command argv")
	}
	m.mu.Lock()
	if m.runningCount() >= m.max {
		m.mu.Unlock()
		return "", m.capError()
	}
	id := m.nextID("bg")
	ctx, cancel := context.WithCancel(context.Background())
	out := &jobSink{
		ring: newRingBuffer(64 * 1024),
		// Guard m.rt != nil: command tests use newJobManager(nil, 8). See
		// finishCommand for why only the command paths do this.
		emit: func(c string) {
			if m.rt != nil {
				m.rt.emitJob(JobProgress{
					JobID: id, Parent: parentName(parent),
					Kind: JobCommand, Title: command, Chunk: c,
				})
			}
		},
	}
	// Best-effort log beside the parent's transcript, so output up to the cap
	// outlives the in-memory ring for the completion mail and task_status.
	var logPath string
	if parent != nil && m.rt != nil && m.rt.store != nil {
		if sid := parent.sess.ID(); sid != "" {
			if p := m.rt.store.JobLogPath(sid, id); p != "" {
				if w := newCappedFileWriter(p); w != nil {
					out.file = w
					logPath = p
				}
			}
		}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	chat.ConfigureGroupKill(cmd, bgWaitDelay)
	j := &bgJob{
		id: id, kind: JobCommand, title: command, parent: parent,
		parentID:      parentName(parent),
		parentSession: parentSessionID(parent),
		startedAt:     time.Now(), cancel: cancel, out: out,
		report: report, note: note, logPath: logPath,
	}
	m.jobs[id] = j
	m.mu.Unlock()

	// Marker before Start: the goroutine that clears it cannot run before the
	// process exists, so the write never races the delete.
	var ownerID string
	if parent != nil {
		ownerID = parent.ID()
	}
	m.putRunningMarker(j, ownerID)

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		delete(m.jobs, id)
		mid := j.markerID
		m.mu.Unlock()
		m.deleteOutboxRow(mid)
		cancel()
		// finishCommand never runs for this job — release the log fd here.
		if out.file != nil {
			out.file.Close()
		}
		return "", err
	}

	m.wg.Add(1)
	go func() {
		var exit int
		defer func() {
			// A panic is a runtime bug, not a command failure: surface it in the
			// output with a nonzero exit rather than reporting a clean done.
			if r := recover(); r != nil {
				exit = -1
				fmt.Fprintf(j.out, "\npanic in job runtime: %v\n", r)
			}
			m.finishCommand(j, exit)
			m.wg.Done()
		}()
		if err := cmd.Wait(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exit = ee.ExitCode()
			} else {
				exit = -1 // pipe/wait failure, not a process exit
			}
		}
	}()
	return id, nil
}

// bgNoticeTailCap bounds the output tail a bg_done notice carries: enough for
// a typical result to be usable, with task_status <id> for the rest.
const bgNoticeTailCap = 1500

// finishCommand delivers a command job's completion, marks the job done, and
// retains it for post-completion inspection.
//
// Routing depends on who started the job:
//   - Root session: one CompletionEvent through dispatchCompletion — the
//     deterministic mail router (floor post on failure, raw post for direct,
//     agent mail otherwise; see completion.go).
//   - Subagent child session: the notice is injected into the still-open child
//     and a follow-up driver resumes it (see runFollowUps) — unless follow-ups
//     are unavailable (child closed, cap reached, poisoned, runtime closing),
//     in which case the job is an ORPHAN: a plain completion event labeled
//     with its origin, threaded at the subagent's root session.
func (m *jobManager) finishCommand(j *bgJob, exit int) {
	// Flush+close the on-disk log before any notice points a reader at it.
	if j.out != nil && j.out.file != nil {
		j.out.file.Close()
	}
	outStr := j.out.String()
	e := exit
	n := notifyBg(j.id, j.title, &e, strutil.Tail(outStr, bgNoticeTailCap))

	m.mu.Lock()
	owner := m.owningSubagentLocked(j.parent)
	var deliver func()
	switch {
	case j.parent == nil:
		deliver = func() {}
	case owner == nil:
		// Root-session job: one deterministic router for direct and default
		// alike (commandEvent carries j.report).
		ev := commandEvent(j, n, exit, j.parent)
		deliver = func() { m.dispatchCompletion(ev) }
	case m.canFollowUpLocked(owner):
		// Child-owned job with follow-ups available: inject into the child (no
		// wake — nothing consumes child wakes) and ensure a driver is running.
		// Injection AND the driver check happen under m.mu so an exiting driver
		// (which re-checks the inbox under the same lock) can never miss it.
		// If the child's main turn is still in flight, the notice just queues:
		// the spawn goroutine's end-of-turn logic starts the driver.
		owner.child.injectNoticeNoWake(n)
		if owner.lingering && !owner.driver {
			owner.driver = true
			m.wg.Add(1)
			go m.runFollowUps(owner)
		}
		deliver = func() {}
	default:
		// Orphaned child-owned job (follow-ups exhausted / poisoned / child
		// closed): a plain completion event labeled with its origin, threaded at
		// the subagent's root session so a wake verdict lands somewhere sane.
		n.Status = "started by subagent " + owner.id
		ev := commandEvent(j, n, exit, owner.parent)
		ev.CronJob = owner.cronJob // an orphan of a cron subagent still posts as ⏰
		ev.Note = joinNote(ev.Note, "started by subagent "+owner.id)
		deliver = func() { m.dispatchCompletion(ev) }
	}
	// Deliver BEFORE markDone, as finishSubagent does: while the notice is in
	// flight the job still counts as running, so a caller gating on
	// runningJobIDs sees it, and once it leaves that set the notice is
	// already queued. The reverse order lets the gate pass and then drops a
	// stale notice into a freshly cleared session. deliver() runs outside
	// m.mu — lock order is session → jobs, never the reverse.
	m.mu.Unlock()
	deliver()
	m.clearRunningMarker(j)
	ex := exit
	m.mu.Lock()
	m.markDoneLocked(j, func(j *bgJob) { j.exit = &ex })
	m.mu.Unlock()
	m.maybeCloseChild(owner)
	j.cancel() // always set before the job is published; release the ctx
	// Command paths guard m.rt != nil because command-only tests construct
	// newJobManager(nil, 8). Subagent paths do not, because startSubagent
	// errors out on a nil rt before a subagent can exist. Do not "fix" this
	// asymmetry.
	if m.rt != nil {
		m.rt.emitJob(JobProgress{
			JobID: j.id, Parent: j.parentID,
			Kind: JobCommand, Title: j.title, Done: true,
		})
		// This may have brought the running-task count to zero; let any Parts
		// a Reload parked close now.
		m.rt.drainParkedClosers()
	}
}

func (m *jobManager) list() []JobInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobInfo, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, JobInfo{
			ID: j.id, Cmd: j.title, Agent: j.agent, StartedAt: j.startedAt,
			Kind: j.kind, ParentID: j.parentID, ParentSession: j.parentSession,
			Done: j.finished, Exit: j.exit, Summary: j.summary,
			Error: j.errText, EndedAt: j.endedAt,
			ChildOpen: j.kind == JobSubagent && j.child != nil && !j.childClosed,
		})
	}
	slices.SortFunc(out, func(a, b JobInfo) int {
		switch {
		case !a.Done && b.Done:
			return -1 // running before done
		case a.Done && !b.Done:
			return 1
		case a.Done:
			return b.EndedAt.Compare(a.EndedAt) // most recently finished first
		default:
			return b.StartedAt.Compare(a.StartedAt)
		}
	})
	return out
}

func (m *jobManager) output(id string) string {
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	if j != nil && j.out != nil {
		return j.out.String()
	}
	return ""
}

func (m *jobManager) cancel(id string) error {
	// Copy under the lock: finishers write j.finished under m.mu.
	m.mu.Lock()
	j := m.jobs[id]
	var finished bool
	var cancelFn context.CancelFunc
	var cascades []context.CancelFunc
	if j != nil {
		finished, cancelFn = j.finished, j.cancel
		// Cancelling a subagent cascades: poison follow-ups and cancel the
		// bash_bg jobs its child started, so task_cancel tears the whole
		// delegation down instead of orphaning jobs. The child closes through
		// the normal paths.
		if j.kind == JobSubagent && j.child != nil {
			j.noFollowUps = true
			for _, cj := range m.jobs {
				if cj.kind == JobCommand && cj.parent == j.child && !cj.finished {
					cascades = append(cascades, cj.cancel)
				}
			}
		}
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Errorf("no such task %q", id)
	}
	for _, c := range cascades {
		c()
	}
	if finished {
		return nil // already done (cascade above still applies to a lingerer)
	}
	if cancelFn != nil {
		cancelFn()
	}
	return nil
}

func (m *jobManager) cancelAll() {
	m.mu.Lock()
	// The runtime is going away: no new drivers, and in-flight completions
	// take the degrade path.
	m.closing = true
	jobs := make([]*bgJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		if !j.finished {
			// Mark before cancelling, so the finish site can tell a
			// shutdown-manufactured failure from one that raced SIGTERM.
			j.shutdownCancel = true
			jobs = append(jobs, j)
		}
	}
	m.mu.Unlock()
	m.driverCancel() // aborts any follow-up turn currently in flight
	for _, j := range jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
}

// killAllForStop kills every live job — the runtime is one pool — with
// completion routing suppressed, and reports what it killed. Follow-ups are
// poisoned as in a task_cancel cascade, but unlike cancelAll the manager is
// NOT marked closing, so new work can start immediately.
func (m *jobManager) killAllForStop() []KilledJob {
	m.mu.Lock()
	var killed []KilledJob
	var cancels []context.CancelFunc
	for _, j := range m.jobs {
		// A lingering subagent is not idle: its child keeps running follow-up
		// turns for every bash_bg job it owns. Skipping it would let one fire,
		// and route normally, minutes after the user was told everything
		// stopped. Poison every live job AND every lingerer, as runningJobIDs
		// counts them.
		lingering := j.kind == JobSubagent && j.child != nil && !j.childClosed
		if j.finished && !lingering {
			continue
		}
		// suppress drops any completion this job still emits, including a
		// lingerer's in-flight follow-up; noFollowUps stops a new driver.
		j.suppress = true
		j.noFollowUps = true
		if j.finished {
			continue // lingerer: poisoned, but no live context to cancel or report
		}
		var ran time.Duration
		if !j.startedAt.IsZero() {
			ran = time.Since(j.startedAt).Round(time.Second)
		}
		killed = append(killed, KilledJob{ID: j.id, Title: j.title, Kind: j.kind.String(), Runtime: ran})
		if j.cancel != nil {
			cancels = append(cancels, j.cancel)
		}
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	slices.SortFunc(killed, func(a, b KilledJob) int { return strings.Compare(a.ID, b.ID) })
	return killed
}

// suppressed reports whether ev's job was killed by superstop (routing drop).
func (m *jobManager) suppressed(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	return j != nil && j.suppress
}

// wait blocks until every job goroutine has unwound. Call after cancelAll,
// before the store closes.
func (m *jobManager) wait() { m.wg.Wait() }

// runningJobIDs lists jobs with live work attached: unfinished ones of either
// kind, plus lingering subagents. Sorted. drainParkedClosers uses it to keep
// an old config generation alive until everything that might touch it drains.
func (m *jobManager) runningJobIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for _, j := range m.jobs {
		if !j.finished || (j.kind == JobSubagent && j.child != nil && !j.childClosed) {
			ids = append(ids, j.id)
		}
	}
	slices.Sort(ids)
	return ids
}

// subagentOpts tunes a subagent job spawned via startSubagent.
type subagentOpts struct {
	workDir  string            // child workdir; "" → the parent session's workdir
	report   notify.ReportMode // what the finish does to the chat
	note     string            // context carried into the completion mail ("" = none)
	cronJob  string            // cron dispatches: the job name ("" for task-tool spawns)
	detached bool              // deliver to the user only; the owning session is told nothing
}

// resolveChildWorkDir picks a subagent's workdir: the override when set (a
// relative one joins onto the parent's base), else the parent's own. An empty
// base means the parent runs at the runtime root, which anchors the join.
func resolveChildWorkDir(parentWD, override, root string) string {
	base := parentWD
	if base == "" {
		base = root
	}
	if override == "" {
		return parentWD // keep the parent's exact value ("" → root downstream)
	}
	if filepath.IsAbs(override) {
		return override
	}
	return filepath.Join(base, override)
}

// startSubagent runs prompt in a new in-process child session,
// asynchronously; finishSubagent then routes the result as completion mail.
func (m *jobManager) startSubagent(parent *Session, agent, prompt, desc string, o subagentOpts) (string, error) {
	if m.rt == nil {
		return "", fmt.Errorf("subagents require a runtime")
	}
	m.mu.Lock()
	if m.runningCount() >= m.max {
		m.mu.Unlock()
		return "", m.capError()
	}
	id := m.nextID("sub")
	// Create cancel BEFORE publishing the job: cancelAll and the finishers
	// invoke j.cancel without the lock, which is safe only if it is written
	// once before the job appears in m.jobs.
	ctx, cancel := context.WithCancel(context.Background())
	// Reserve the slot before releasing the lock, or two spawns at max-1 both
	// pass the cap check.
	pname := parentName(parent)
	out := &jobSink{
		ring: newRingBuffer(64 * 1024),
		emit: func(c string) {
			m.rt.emitJob(JobProgress{
				JobID: id, Parent: pname,
				Kind: JobSubagent, Title: desc, Chunk: c,
			})
		},
	}
	j := &bgJob{
		id: id, kind: JobSubagent, title: desc, agent: agent, parent: parent,
		parentID: pname, parentSession: parentSessionID(parent), startedAt: time.Now(),
		cancel: cancel, out: out,
		report: o.report, note: o.note, cronJob: o.cronJob, detached: o.detached,
	}
	m.jobs[id] = j
	m.mu.Unlock()

	// An agent may declare its own workdir; an explicit spawn-time one wins.
	if o.workDir == "" {
		if parts := m.rt.Parts(); parts != nil {
			o.workDir = parts.SubagentWorkdir(agent)
		}
	}

	// Entirely in-process: when the child's event stream drains, the goroutine
	// below calls finishSubagent. The child holds no back-reference — it is an
	// ordinary headless session whose only tie to the job is j.childID.
	child, err := m.rt.Session(SessionOpts{
		Agent:    agent,
		WorkDir:  resolveChildWorkDir(parent.opts.WorkDir, o.workDir, m.rt.workDir),
		Headless: true,
		// Record the parent so the child's row reads as a subagent transcript
		// rather than a conversation: the dash groups by it, and the janitor
		// spares a message-less session other rows name as parent.
		ParentID: parent.sess.ID(),
		CronJob:  o.cronJob,
	})
	if err != nil {
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		cancel() // release context resources; goroutine was never started
		return "", err
	}
	m.mu.Lock()
	j.childID = child.sess.ID()
	j.child = child
	m.mu.Unlock()

	// Marker before the goroutine starts, so the finish path cannot race it.
	m.putRunningMarker(j, parent.ID())

	m.wg.Add(1)
	go func() {
		var summary string
		var runErr error
		defer func() {
			// A panic escaping the event loop marks the job failed, never a
			// clean done with partial output.
			if r := recover(); r != nil {
				runErr = fmt.Errorf("panic in subagent runtime: %v", r)
			}
			// A cancelled job may never see a terminal Error event — route
			// drops it on an abandoned channel — so consult the ctx directly.
			if runErr == nil && ctx.Err() != nil {
				runErr = ctx.Err()
			}
			errText := ""
			if runErr != nil {
				errText = runErr.Error()
			}
			// Report done first, then decide whether the child closes or
			// lingers for its still-running bash_bg jobs.
			m.finishSubagent(j, summary, errText)
			m.endSubagentTurn(j, child, errText)
			m.wg.Done()
		}()
		summary, runErr = consumeChildEvents(j, child.Send(ctx, prompt))
	}()
	return id, nil
}

// consumeChildEvents drains one child turn's events into j.out, so the Jobs
// view shows live progress before the stored transcript is readable. Returns
// the final assistant text and the last error. Shared by main and follow-up
// turns.
func consumeChildEvents(j *bgJob, events <-chan Event) (summary string, runErr error) {
	var last strings.Builder
	for ev := range events {
		switch ev.Kind {
		case Token:
			last.WriteString(ev.Text)
			_, _ = j.out.Write([]byte(ev.Text))
		case ToolCall:
			fmt.Fprintf(j.out, "\n\n$ %s %s\n", ev.ToolName, ev.ToolInput)
		case ToolResult:
			last.Reset() // the next assistant message starts fresh
			res := ev.ToolOutput
			if len(res) > 2000 {
				res = strutil.Truncate(res, 2000) + "(truncated)"
			}
			fmt.Fprintf(j.out, "%s\n", res)
		case Error:
			// Remember the last error so the caller reports a failed run
			// rather than a clean done with partial output.
			if ev.Err != nil {
				runErr = ev.Err
				fmt.Fprintf(j.out, "\nerror: %v\n", ev.Err)
			}
		}
	}
	return strings.TrimSpace(last.String()), runErr
}

// endSubagentTurn decides the child session's fate once the main turn ended
// and agent_done was delivered:
//
//   - failed/cancelled turn (errText != ""): follow-ups are poisoned and any
//     still-running bash_bg jobs the child started are cascade-cancelled; the
//     child closes once none remain.
//   - live jobs or queued completion notices with follow-ups available: the
//     child LINGERS (stays open) so each completion can resume it — see
//     runFollowUps.
//   - otherwise: the child closes immediately (the common case).
func (m *jobManager) endSubagentTurn(j *bgJob, child *Session, errText string) {
	m.mu.Lock()
	j.lingering = true
	if errText != "" {
		j.noFollowUps = true
	}
	canFollow := m.canFollowUpLocked(j)
	var cancels []context.CancelFunc
	if !canFollow {
		for _, cj := range m.jobs {
			if cj.kind == JobCommand && cj.parent == child && !cj.finished {
				cancels = append(cancels, cj.cancel)
			}
		}
	}
	running := m.childRunningJobsLocked(child)
	if canFollow && !j.driver && child.HasQueuedInput() {
		// A job finished mid-turn and its notice is queued: resume now.
		j.driver = true
		m.wg.Add(1)
		go m.runFollowUps(j)
	}
	closeNow := !j.driver && running == 0 && (!canFollow || !child.HasQueuedInput())
	if closeNow {
		j.childClosed = true
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c() // cancelled jobs finish via finishCommand → degrade path → maybeCloseChild
	}
	if closeNow {
		_ = child.Close()
		// The child may have been the last live work on a parked generation.
		if m.rt != nil {
			m.rt.drainParkedClosers()
		}
	}
}

// canFollowUpLocked reports whether sub may still run follow-ups. Under m.mu.
func (m *jobManager) canFollowUpLocked(sub *bgJob) bool {
	return sub != nil && sub.child != nil && !sub.childClosed && !sub.noFollowUps &&
		!m.closing && sub.followUps < maxFollowUps
}

// owningSubagentLocked resolves a parent session to the subagent job whose
// child it is, nil for a root session. Under m.mu.
func (m *jobManager) owningSubagentLocked(sess *Session) *bgJob {
	if sess == nil {
		return nil
	}
	for _, j := range m.jobs {
		if j.kind == JobSubagent && j.child == sess {
			return j
		}
	}
	return nil
}

// childRunningJobsLocked counts unfinished command jobs under sess. Under m.mu.
func (m *jobManager) childRunningJobsLocked(sess *Session) int {
	n := 0
	for _, j := range m.jobs {
		if j.kind == JobCommand && j.parent == sess && !j.finished {
			n++
		}
	}
	return n
}

// runFollowUps is the one driver for a lingering subagent: it resumes the
// child over its queued notices and routes each summary like any completion.
// At most one driver per job (sub.driver); its exit re-check holds the same
// lock finishCommand injects under, so a completion cannot slip between
// "inbox empty" and "driver gone". A turn error poisons further follow-ups,
// and still reaches the root in the update's status.
func (m *jobManager) runFollowUps(sub *bgJob) {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		if !m.canFollowUpLocked(sub) || !sub.child.HasQueuedInput() {
			sub.driver = false
			m.mu.Unlock()
			break
		}
		sub.followUps++
		child := sub.child
		m.mu.Unlock()

		fmt.Fprintf(sub.out, "\n\n[follow-up turn: a background job finished]\n")
		summary, runErr := consumeChildEvents(sub, child.RunQueued(m.driverCtx))
		errText := ""
		if runErr != nil {
			errText = strutil.Truncate(runErr.Error(), 200)
			m.mu.Lock()
			sub.noFollowUps = true
			m.mu.Unlock()
		}
		if sub.parent == nil || m.rt == nil {
			continue
		}
		n := notify.Notification{
			Kind: notify.KindAgentUpdate, ID: sub.id, Preview: summary,
			TS: time.Now().UTC().Format(time.RFC3339),
		}
		if errText != "" {
			n.Status = "error: " + errText
		}
		m.dispatchCompletion(followUpEvent(sub, n, summary, errText))
	}
	m.maybeCloseChild(sub)
}

// maybeCloseChild closes a lingering child session once nothing can happen to
// it anymore: no driver active, no running jobs, and no queued notices a
// future driver could consume. Safe to call with a nil job or from any path;
// it re-checks everything under m.mu and is a no-op unless the close
// conditions hold. If a queued notice arrived while the driver was exiting
// (or via the degrade path racing a poison), it starts a fresh driver instead
// of closing, so no completion is silently dropped while follow-ups remain
// available.
func (m *jobManager) maybeCloseChild(sub *bgJob) {
	if sub == nil {
		return
	}
	m.mu.Lock()
	if sub.child == nil || sub.childClosed || sub.driver || !sub.lingering {
		m.mu.Unlock()
		return
	}
	if m.childRunningJobsLocked(sub.child) > 0 {
		m.mu.Unlock()
		return
	}
	if sub.child.HasQueuedInput() && m.canFollowUpLocked(sub) {
		sub.driver = true
		m.wg.Add(1)
		go m.runFollowUps(sub)
		m.mu.Unlock()
		return
	}
	sub.childClosed = true
	child := sub.child
	m.mu.Unlock()
	_ = child.Close()
	// The child leaving the running set may have drained the last live work on
	// an old generation a Reload deferred.
	if m.rt != nil {
		m.rt.drainParkedClosers()
	}
}

// finishSubagent routes a subagent's completion, marks the job done, and
// retains it for post-completion transcript reads. A non-empty errText marks
// the job as failed: the event, task_list, and task_status all report "error"
// instead of a clean "done".
//
// Routing: raw-report jobs wake the parent with a KindAgentDone notice (cron
// raw jobs, whose pinned parent never runs turns, hand the result to a
// fresh main-agent turn via the CompletionHost instead); everything else is a
// completion event through the mail router.
func (m *jobManager) finishSubagent(j *bgJob, summary, errText string) {
	if j.parent != nil {
		m.dispatchCompletion(subagentEvent(j, summary, errText))
	}
	m.mu.Lock()
	m.markDoneLocked(j, func(j *bgJob) { j.summary, j.errText = summary, errText })
	m.mu.Unlock()
	m.clearRunningMarker(j)
	j.cancel() // always set before the job is published; release the ctx
	m.rt.emitJob(JobProgress{
		JobID: j.id, Parent: j.parentID,
		Kind: JobSubagent, Title: j.title, Done: true, Summary: summary,
	})
	// A lingering child keeps this subagent in the running set, so the drain
	// no-ops until the close paths run it again.
	m.rt.drainParkedClosers()
}

// markDoneLocked is the bookkeeping both finishers share: mark finished,
// apply the kind-specific fields via set, stamp endedAt, evict past the cap.
// Under m.mu.
func (m *jobManager) markDoneLocked(j *bgJob, set func(*bgJob)) {
	j.finished = true
	set(j)
	j.endedAt = time.Now()
	m.evictOldestDoneIfNeeded()
}

// notifyAgentDone builds a subagent's agent_done notification. A non-empty
// errText rides truncated in Status, so the notice reads "finished (error: …)".
func notifyAgentDone(id, summary, errText string) notify.Notification {
	n := notify.Notification{
		Kind:    notify.KindAgentDone,
		ID:      id, // the job id (sub1), matching the spawn message + task_* tools
		Preview: summary,
		TS:      time.Now().UTC().Format(time.RFC3339),
	}
	if errText != "" {
		n.Status = "error: " + strutil.Truncate(errText, 200)
	}
	return n
}

// transcript reads the child session's stored messages, nil when
// unavailable. Works during the run and after, since the job is retained.
func (m *jobManager) transcript(id string) []llm.Message {
	m.mu.Lock()
	var childID string
	if j := m.jobs[id]; j != nil {
		childID = j.childID
	}
	m.mu.Unlock()
	if childID == "" || m.rt == nil || m.rt.store == nil {
		return nil
	}
	msgs, err := m.rt.store.LoadMessages(childID)
	if err != nil {
		return nil
	}
	return msgs
}

// parentName returns the session's registry name, or "" for a nil parent.
func parentName(s *Session) string {
	if s == nil {
		return ""
	}
	return s.name
}

// parentSessionID is the parent's runs session id, the directory its jobs'
// logs live under; "" with no parent or store.
func parentSessionID(s *Session) string {
	if s == nil {
		return ""
	}
	return s.sess.ID()
}

// formatJobList lists every job for task_list: one line each, running first.
func (m *jobManager) formatJobList() string {
	jobs := m.list() // already sorted: running first, then most-recently-done first
	if len(jobs) == 0 {
		return "no background tasks"
	}
	var b strings.Builder
	b.WriteString("background tasks:\n")
	for _, j := range jobs {
		kind := j.Kind.String()
		if j.Kind == JobSubagent && j.Agent != "" {
			kind = "@" + j.Agent
		}
		fmt.Fprintf(&b, "  %s  %s  %s", j.ID, kind, jobStatusLabel(j.Done, j.Exit, j.Error))
		if j.Kind == JobSubagent && j.Cmd != "" {
			fmt.Fprintf(&b, "  — %s", j.Cmd)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderTranscriptText flattens a child's stored messages for task_status.
// System messages are skipped; a tool call lists its name and a tool result a
// one-line label, so the model sees that one arrived without the full output.
func renderTranscriptText(msgs []llm.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		switch msg.Role {
		case llm.RoleSystem:
			// skip — system prompts are not useful in a status summary
		case llm.RoleUser:
			if t := strings.TrimSpace(msg.Content); t != "" {
				fmt.Fprintf(&b, "user: %s\n", t)
			}
		case llm.RoleAssistant:
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&b, "tool_call: %s\n", tc.Name)
			}
			if t := strings.TrimSpace(msg.Content); t != "" {
				fmt.Fprintf(&b, "assistant: %s\n", t)
			}
		case llm.RoleTool:
			fmt.Fprintf(&b, "tool_result: [%s]\n", msg.Name)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// jobStatusCap bounds formatJobStatus so a huge result cannot flood context.
const jobStatusCap = 4000

// formatJobStatus renders one job's status and truncated result.
func (m *jobManager) formatJobStatus(id string) string {
	// Copy under the lock: the finishers write these fields under m.mu.
	m.mu.Lock()
	j := m.jobs[id]
	var (
		jKind    JobKind
		finished bool
		exit     *int
		summary  string
		errText  string
		polls    int
		childID  string
	)
	if j != nil {
		jKind = j.kind
		finished, exit = j.finished, j.exit
		summary, errText = j.summary, j.errText
		childID = j.childID
		if !finished {
			j.statusPolls++
			polls = j.statusPolls
		}
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Sprintf("no such task %q", id)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "task %s: %s (%s)\n", id, jobStatusLabel(finished, exit, errText), jKind)
	// The child's runs id, so the caller can read what the subagent actually
	// did rather than judge it by its summary — reviewing the transcript is
	// how you find out it took a shortcut, and it is unreachable by name
	// without this.
	if childID != "" {
		fmt.Fprintf(&b, "transcript: %s  (read it with the history tool: {\"session\": \"%s\"})\n", childID, childID)
	}

	if jKind == JobSubagent {
		if errText != "" {
			fmt.Fprintf(&b, "error: %s\n", strutil.Truncate(errText, 500))
		}
		if summary != "" {
			fmt.Fprintf(&b, "summary: %s\n", strutil.Truncate(summary, 2000))
		}
		// Prefer the stored transcript once the run ends; while it runs that
		// does not exist, so fall back to the live buffer for progress.
		msgs := m.transcript(id)
		body, label := renderTranscriptText(msgs), "transcript"
		if len(msgs) == 0 {
			body, label = m.output(id), "progress"
		}
		appendCappedTail(&b, label, body)
	} else {
		appendCappedTail(&b, "output", m.output(id))
		m.mu.Lock()
		logPath := j.logPath
		m.mu.Unlock()
		if logPath != "" {
			fmt.Fprintf(&b, "\nfull output: %s", logPath)
		}
	}
	// A second check on the same running job is a poll loop forming: checking
	// cannot finish the work, and the loop burns the turn the wake mechanism
	// exists to free. Host text, because "do not poll" alone does not hold.
	if !finished && polls >= 2 {
		fmt.Fprintf(&b, "\nStill running, and you have now checked %d times — polling cannot finish it. "+
			"Stop checking and end your turn (reply to the user; say the job is running). "+
			"Its completion will wake you: as part of this reply if it finishes before the turn ends, "+
			"as a new message otherwise. Do not sleep-and-recheck in bash either.", polls)
	}
	return strings.TrimRight(b.String(), "\n")
}

// jobStatusLabel is a job's one-word status: running, done, error, or
// error(exit N) for a command.
func jobStatusLabel(finished bool, exit *int, errText string) string {
	switch {
	case !finished:
		return "running"
	case errText != "":
		return "error"
	case exit != nil && *exit != 0:
		return fmt.Sprintf("error(exit %d)", *exit)
	default:
		return "done"
	}
}

// appendCappedTail appends body under a "label:" header within what is left of
// jobStatusCap, keeping a rune-safe tail with a marker when it does not fit
// and appending nothing when the headers alone spent the budget.
func appendCappedTail(b *strings.Builder, label, body string) {
	if body == "" {
		return
	}
	// Reserve room for the header and marker so the tail budget stays positive.
	const overhead = 20
	remaining := jobStatusCap - b.Len() - overhead
	if remaining <= 0 {
		return
	}
	if len(body) > remaining {
		b.WriteString(label + " tail:\n")
		b.WriteString(strutil.Tail(body, remaining))
		b.WriteString("\n…(truncated)")
	} else {
		b.WriteString(label + ":\n")
		b.WriteString(body)
	}
}

// formatJobCancel cancels a job and returns a short confirmation or error string.
func (m *jobManager) formatJobCancel(id string) string {
	err := m.cancel(id)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("cancelled task %s", id)
}
