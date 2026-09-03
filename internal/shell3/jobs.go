package shell3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/procutil"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/strutil"
)

// JobKind discriminates an in-process background job's payload.
type JobKind int

const (
	JobCommand JobKind = iota
)

// String returns the job kind used in durable markers.
func (k JobKind) String() string {
	if k == JobCommand {
		return "command"
	}
	return fmt.Sprintf("JobKind(%d)", int(k))
}

const defaultMaxConcurrent = 8

// bgWaitDelay bounds cmd.Wait on the stdio pipes after a cancel. Background
// jobs are off the turn's critical path, so they get a little longer to drain.
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

// jobSink tees command output to memory, progress events, and an optional log.
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
	title    string
	parent   *Session
	parentID string
	// parentSession is the parent's RUNS session id, the directory a job log
	// lives under — not parentID, the in-process handle ("s1"). A front-end
	// linking to the log needs this one; they are not interchangeable.
	parentSession string
	startedAt     time.Time
	cancel        context.CancelFunc
	out           *jobSink
	// report is the single axis for what this job's finish does to the chat:
	// raw output, a report turn the agent may answer, or one it must.
	report   notify.ReportMode
	detached bool
	// note is the spawner's intent hint, carried into the completion mail.
	note string
	// logPath is runs/<parent-session>/jobs/<id>.log, "" when no store or
	// parent existed at start. Completion mail points here.
	logPath string
	// store is the configuration-generation handle captured at dispatch.
	store *runs.Store

	// suppress drops completions already covered by /superstop.
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
	exit     *int      // nil while running
	endedAt  time.Time // zero while running
}

type jobManager struct {
	mu   sync.Mutex
	wg   sync.WaitGroup // tracks live job goroutines for Close ordering
	rt   *Runtime
	jobs map[string]*bgJob
	max  int
	seq  int

	// closing is set by cancelAll so shutdown cannot admit new work.
	closing bool

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
	return &jobManager{
		rt: rt, jobs: map[string]*bgJob{},
		max: maxConcurrent,
	}
}

// nextID must be called under m.mu.
func (m *jobManager) nextID(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s%d", prefix, m.seq)
}

func (m *jobManager) capError() error {
	return fmt.Errorf("background-job cap %d reached; wait for a job to finish", m.max)
}

// runningCount must be called under m.mu.
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
	if m.closing {
		m.mu.Unlock()
		return "", errors.New("background jobs are shutting down")
	}
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
	// outlives the in-memory ring and remains inspectable with Bash.
	var logPath string
	jobStore := sessionStore(parent)
	if parent != nil && jobStore != nil {
		if sid := parent.sess.ID(); sid != "" {
			if p := jobStore.JobLogPath(sid, id); p != "" {
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
	procutil.ConfigureGroupCancel(cmd, bgWaitDelay)
	j := &bgJob{
		id: id, kind: JobCommand, title: command, parent: parent,
		parentID:      parentName(parent),
		parentSession: parentSessionID(parent),
		startedAt:     time.Now(), cancel: cancel, out: out,
		report: report, note: note, logPath: logPath, store: jobStore,
	}
	m.jobs[id] = j
	// Count the job before publishing it outside m.mu. cancelAll takes this
	// same lock before calling Wait, so shutdown can never observe a job whose
	// positive Add has not happened yet.
	m.wg.Add(1)
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
		m.wg.Done()
		return "", err
	}

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

// bgNoticeTailCap bounds the output tail carried into the next turn.
const bgNoticeTailCap = 1500

// finishCommand delivers a command completion, marks it done, and retains it
// for front-end inspection.
func (m *jobManager) finishCommand(j *bgJob, exit int) {
	if j.out != nil && j.out.file != nil {
		j.out.file.Close()
	}
	outStr := j.out.String()
	e := exit
	n := notifyBg(j.id, j.title, &e, strutil.Tail(outStr, bgNoticeTailCap), j.logPath)
	if j.parent != nil {
		m.dispatchCompletion(commandEvent(j, n, exit, j.parent))
	}
	m.clearRunningMarker(j)
	ex := exit
	m.mu.Lock()
	m.markDoneLocked(j, func(j *bgJob) { j.exit = &ex })
	m.mu.Unlock()
	j.cancel()
	if m.rt != nil {
		m.rt.emitJob(JobProgress{
			JobID: j.id, Parent: j.parentID,
			Kind: JobCommand, Title: j.title, Done: true,
		})
	}
}

func (m *jobManager) list() []JobInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobInfo, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, JobInfo{
			ID: j.id, Cmd: j.title, StartedAt: j.startedAt,
			Kind: j.kind, ParentID: j.parentID, ParentSession: j.parentSession,
			Done: j.finished, Exit: j.exit, EndedAt: j.endedAt,
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

func (m *jobManager) cancelAll() {
	m.mu.Lock()
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
	for _, j := range jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
}

// killAllForStop kills every live command with completion routing suppressed.
func (m *jobManager) killAllForStop() []KilledJob {
	m.mu.Lock()
	var killed []KilledJob
	var cancels []context.CancelFunc
	for _, j := range m.jobs {
		if j.finished {
			continue
		}
		j.suppress = true
		var ran time.Duration
		if !j.startedAt.IsZero() {
			ran = time.Since(j.startedAt).Round(time.Second)
		}
		killed = append(killed, KilledJob{ID: j.id, Title: j.title, Kind: "command", Runtime: ran})
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

func (m *jobManager) markDoneLocked(j *bgJob, set func(*bgJob)) {
	set(j)
	j.finished = true
	j.endedAt = time.Now()
	m.evictOldestDoneIfNeeded()
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
