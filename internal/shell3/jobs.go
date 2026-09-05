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

	"github.com/weatherjean/shell3/internal/procutil"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/strutil"
)

const defaultMaxConcurrent = 8

// bgWaitDelay bounds cmd.Wait on the stdio pipes after a cancel. Background
// jobs are off the turn's critical path, so they get a little longer to drain.
const bgWaitDelay = 3 * time.Second

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

// jobSink tees command output to memory and an optional durable log.
type jobSink struct {
	ring *ringBuffer
	file *cappedFileWriter
}

func (s *jobSink) Write(p []byte) (int, error) {
	n, err := s.ring.Write(p)
	if s.file != nil {
		s.file.Write(p)
	}
	return n, err
}

// String is the accumulated ring-buffer content, the interface bgJob.out
// callers rely on.
func (s *jobSink) String() string { return s.ring.String() }

type bgJob struct {
	id        string
	title     string
	parentID  string
	startedAt time.Time
	cancel    context.CancelFunc
	out       *jobSink
	// logPath is runs/<parent-session>/jobs/<id>.log, "" when no store or
	// parent existed at start. The inbox notice points here.
	logPath string
	// store is the configuration-generation handle captured at dispatch.
	store *runs.Store

	// suppress drops completions already covered by /superstop.
	suppress bool

	// shutdownCancel marks a job cancelAll killed on teardown: its "context
	// canceled" failure was manufactured by the shutdown, so no notice is
	// written and the running marker survives instead — the next boot then
	// reports it honestly as "was running when shell3 stopped".
	shutdownCancel bool

	// markerID is the job's background_jobs row (0 = none), deleted when the
	// job finishes unless shutdownCancel left it for boot-time recovery.
	markerID int64
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
	return len(m.jobs)
}

// startCommand launches argv as a managed background job. env appends "K=V"
// entries to the inherited environment; nil inherits it unchanged.
func (m *jobManager) startCommand(parent *Session, command, workdir string, argv, env []string) (string, error) {
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
	out := &jobSink{ring: newRingBuffer(64 * 1024)}
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
		id: id, title: command, parentID: parentName(parent),
		startedAt: time.Now(), cancel: cancel, out: out,
		logPath: logPath, store: jobStore,
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
		m.deleteRunningMarker(mid, j.store, j.id)
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

// bgNoticeTailCap bounds the output tail stored in the durable inbox notice.
const bgNoticeTailCap = 1500

// finishCommand delivers a command completion and removes its live marker.
func (m *jobManager) finishCommand(j *bgJob, exit int) {
	if j.out != nil && j.out.file != nil {
		j.out.file.Close()
	}
	outStr := j.out.String()
	if m.persistCommandCompletion(j, exit, strutil.Tail(outStr, bgNoticeTailCap)) {
		m.clearRunningMarker(j)
	}
	m.mu.Lock()
	delete(m.jobs, j.id)
	m.mu.Unlock()
	j.cancel()
	if m.rt != nil {
		m.rt.emitJobCompletion()
	}
}

func (m *jobManager) cancelAll() {
	m.mu.Lock()
	m.closing = true
	jobs := make([]*bgJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		// Mark before cancelling, so the finish site can tell a
		// shutdown-manufactured failure from one that raced SIGTERM.
		j.shutdownCancel = true
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	for _, j := range jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
}

// killAllForStop kills every live command with its completion notice suppressed.
func (m *jobManager) killAllForStop() []KilledJob {
	m.mu.Lock()
	var killed []KilledJob
	var cancels []context.CancelFunc
	for _, j := range m.jobs {
		j.suppress = true
		var ran time.Duration
		if !j.startedAt.IsZero() {
			ran = time.Since(j.startedAt).Round(time.Second)
		}
		killed = append(killed, KilledJob{ID: j.id, Title: j.title, Runtime: ran})
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

// wait blocks until every job goroutine has unwound. Call after cancelAll,
// before the store closes.
func (m *jobManager) wait() { m.wg.Wait() }

// parentName returns the session's registry name, or "" for a nil parent.
func parentName(s *Session) string {
	if s == nil {
		return ""
	}
	return s.name
}
