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
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/runs"
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

// bgWaitDelay bounds how long cmd.Wait blocks on the stdio pipes after a
// command job is cancelled (mirrors internal/chat's bashWaitDelay).
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

// jobSink is an io.Writer that tees each write to a ring buffer AND calls an
// emit callback with the chunk string. It is used as cmd.Stdout/cmd.Stderr for
// command jobs and as the subagent event stream sink, so both the :background
// modal and the JobEvents bus receive live output.
type jobSink struct {
	ring *ringBuffer
	emit func(chunk string)
}

func (s *jobSink) Write(p []byte) (int, error) {
	n, err := s.ring.Write(p)
	s.emit(string(p))
	return n, err
}

// String returns the accumulated ring-buffer content, preserving the same
// interface that bgJob.out.String() callers (e.g. output()) rely on.
func (s *jobSink) String() string { return s.ring.String() }

type bgJob struct {
	id        string
	kind      JobKind
	title     string // command text or subagent description
	agent     string // subagent jobs: the spawned agent's name ("" for commands)
	parent    *Session
	parentID  string
	depth     int
	pid       int
	startedAt time.Time
	cancel    context.CancelFunc
	out       *jobSink // live output: command stdout/stderr, or subagent event stream
	childID   string   // subagent: child runs id (transcript source)
	quiet     bool     // subagent: completion notice queues without waking the parent

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

// capError is the shared cap-exceeded error for both job kinds.
func (m *jobManager) capError() error {
	return fmt.Errorf("background-job cap %d reached; wait for a job to finish", m.max)
}

// runningCount returns the number of non-finished jobs. Must be called under m.mu.
func (m *jobManager) runningCount() int {
	n := 0
	for _, j := range m.jobs {
		if !j.finished {
			n++
		}
	}
	return n
}

// evictOldestDoneIfNeeded drops the oldest finished job when the done-job count
// exceeds maxDoneJobs. Must be called under m.mu.
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

// startCommand launches argv as a managed background job. env holds extra
// "K=V" entries appended to the inherited environment (background custom tools
// inject their params this way); nil inherits the environment unchanged.
func (m *jobManager) startCommand(parent *Session, command, workdir string, argv, env []string) (string, error) {
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
		// Guard m.rt != nil: command tests use newJobManager(nil, 8); see
		// finishCommand for the full asymmetry explanation.
		emit: func(c string) {
			if m.rt != nil {
				m.rt.emitJob(JobProgress{
					JobID: id, Parent: parentName(parent),
					Kind: JobCommand, Title: command, Chunk: c,
				})
			}
		},
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	// Put the command and its descendants in their own process group and signal
	// the whole tree on cancel — bare SIGKILL on the shell leaves grandchildren
	// (e.g. a server started with `&`) holding our stdout/stderr pipes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	// Bound how long Wait blocks on the stdio pipes after the command itself
	// exits: without this a lingering grandchild that inherited the pipes keeps
	// Wait (and Runtime.Close) blocked forever, leaking the concurrency slot.
	cmd.WaitDelay = bgWaitDelay
	j := &bgJob{
		id: id, kind: JobCommand, title: command, parent: parent,
		parentID:  parentName(parent),
		startedAt: time.Now(), cancel: cancel, out: out,
	}
	m.jobs[id] = j
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		cancel()
		return "", err
	}
	m.mu.Lock()
	j.pid = cmd.Process.Pid
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		var exit int
		defer func() {
			// A panic here is a runtime bug, not a command failure — surface it in
			// the job output and a nonzero exit instead of reporting a clean done.
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

// finishCommand injects a notice-only completion into the parent (no Wake),
// marks the job done, and retains it for post-completion inspection.
func (m *jobManager) finishCommand(j *bgJob, exit int) {
	outStr := j.out.String()
	if j.parent != nil {
		e := exit
		j.parent.injectNoticeNoWake(notifyBg(j.id, j.title, &e, strutil.Tail(outStr, 400)))
	}
	m.mu.Lock()
	ex := exit
	j.finished = true
	j.exit = &ex
	j.endedAt = time.Now()
	m.evictOldestDoneIfNeeded()
	m.mu.Unlock()
	j.cancel() // always set before the job is published; release the ctx
	// Command paths guard m.rt != nil because command-only tests construct
	// newJobManager(nil, 8) to avoid a full Runtime. Subagent paths (startSubagent,
	// finishSubagent) call m.rt unconditionally because a subagent cannot be
	// created without a non-nil rt (startSubagent calls m.rt.Session first and
	// returns an error if rt is nil). Do not "fix" this asymmetry.
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
			ID: j.id, Cmd: j.title, Agent: j.agent, PID: j.pid, StartedAt: j.startedAt,
			Kind: j.kind, Depth: j.depth, ParentID: j.parentID,
			Done: j.finished, Exit: j.exit, Summary: j.summary,
			Error: j.errText, EndedAt: j.endedAt,
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
	// Copy the fields we need under the lock: finishers write j.finished under
	// m.mu, so reading it after Unlock would race them.
	m.mu.Lock()
	j := m.jobs[id]
	var finished bool
	var cancelFn context.CancelFunc
	if j != nil {
		finished, cancelFn = j.finished, j.cancel
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Errorf("no such task %q", id)
	}
	if finished {
		return nil // already done; cancel is a no-op
	}
	if cancelFn != nil {
		cancelFn()
	}
	return nil
}

func (m *jobManager) cancelAll() {
	m.mu.Lock()
	jobs := make([]*bgJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		if !j.finished {
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

// wait blocks until all active job goroutines have finished. Call after
// cancelAll to ensure goroutines have fully unwound before the store closes.
func (m *jobManager) wait() { m.wg.Wait() }

// subagentOpts tunes a subagent job spawned via startSubagent.
type subagentOpts struct {
	workDir string // child workdir; "" → the parent session's workdir
	quiet   bool   // true → the completion notice does not wake the parent
}

// resolveChildWorkDir picks a subagent job's workdir: the override when set (a
// relative override joins onto the parent's effective base), else the parent's
// own workdir. base "" means the parent runs at the runtime root, so root
// substitutes as the join anchor.
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

// startSubagent creates an in-process child session and runs prompt inside it
// asynchronously. When the child finishes, finishSubagent injects a
// KindAgentDone notification into the parent session and wakes it (unless the
// job is quiet and finished cleanly).
func (m *jobManager) startSubagent(parent *Session, agent, prompt, desc string, depth int, o subagentOpts) (string, error) {
	if m.rt == nil {
		return "", fmt.Errorf("subagents require a runtime")
	}
	m.mu.Lock()
	if m.runningCount() >= m.max {
		m.mu.Unlock()
		return "", m.capError()
	}
	id := m.nextID("sub")
	// Create the cancel func BEFORE publishing the job so j.cancel is
	// immutable-once-visible: cancelAll() (and the finishers) invoke j.cancel
	// without holding the lock, which is safe only when it is written once
	// before the job appears in m.jobs (Fix 4: j.cancel data race).
	ctx, cancel := context.WithCancel(context.Background())
	// Reserve the slot atomically before releasing the lock so that two
	// concurrent spawns at max-1 cannot both pass the cap check (TOCTOU).
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
		parentID: pname, depth: depth, startedAt: time.Now(),
		cancel: cancel, out: out, quiet: o.quiet,
	}
	m.jobs[id] = j
	m.mu.Unlock()

	// Completion is handled entirely in-process: when the child's event stream
	// drains, the goroutine below calls finishSubagent, which injects the
	// KindAgentDone notice into the parent and wakes it. The child session
	// carries no back-reference to the parent — it is an ordinary headless
	// runtime session whose only tie to the job is j.childID (its runs id).
	child, err := m.rt.Session(SessionOpts{
		Agent:    agent,
		Depth:    depth,
		WorkDir:  resolveChildWorkDir(parent.opts.WorkDir, o.workDir, m.rt.workDir),
		Headless: true,
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
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		var last strings.Builder
		var runErr error
		defer func() {
			// A panic escaping the event loop must mark the job failed, not let it
			// finish as a clean "done" with partial output.
			if r := recover(); r != nil {
				runErr = fmt.Errorf("panic in subagent runtime: %v", r)
			}
			// A cancelled job (task_cancel, Runtime.Close) may never see a
			// terminal Error event — route drops it when the channel is being
			// abandoned — so consult the job ctx directly: a cancelled run must
			// report as failed, never as a clean "done" with partial output.
			if runErr == nil && ctx.Err() != nil {
				runErr = ctx.Err()
			}
			_ = child.Close()
			errText := ""
			if runErr != nil {
				errText = runErr.Error()
			}
			m.finishSubagent(j, strings.TrimSpace(last.String()), errText)
			m.wg.Done()
		}()
		for ev := range child.Send(ctx, prompt) {
			// Mirror the child's event stream into j.out so the :background modal
			// can show live progress while the subagent runs (its messages.jsonl
			// transcript is not written until the run ends).
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
				// The turn failed (provider error, cancellation, …). Remember the
				// last error so finishSubagent can report the job as failed instead
				// of announcing a clean "done" with partial output.
				if ev.Err != nil {
					runErr = ev.Err
					fmt.Fprintf(j.out, "\nerror: %v\n", ev.Err)
				}
			}
		}
	}()
	return id, nil
}

// finishSubagent injects a KindAgentDone completion notification into the
// parent (which also wakes it if idle), marks the job done, and retains it
// for post-completion transcript reads. A non-empty errText marks the job as
// failed: the notice, task_list, and task_status all report "error" instead
// of a clean "done".
func (m *jobManager) finishSubagent(j *bgJob, summary, errText string) {
	if j.parent != nil {
		n := notifyAgentDone(j.id, summary, errText)
		// quiet suppresses the wake for clean runs only: a failed job always
		// wakes, so an unattended host (cron notify=false) still surfaces errors.
		if j.quiet && errText == "" {
			j.parent.injectNoticeNoWake(n) // queued for the next turn, no wake
		} else {
			j.parent.injectNotification(m.rt, n) // InterjectNotice + Wake
		}
	}
	m.mu.Lock()
	j.finished = true
	j.summary = summary
	j.errText = errText
	j.endedAt = time.Now()
	m.evictOldestDoneIfNeeded()
	m.mu.Unlock()
	j.cancel() // always set before the job is published; release the ctx
	m.rt.emitJob(JobProgress{
		JobID: j.id, Parent: j.parentID,
		Kind: JobSubagent, Title: j.title, Done: true, Summary: summary,
	})
}

// notifyAgentDone builds an agent_done completion notification for a subagent
// job (the counterpart of transport.go's notifyBg). A non-empty errText marks
// the run as failed; the (truncated) error rides in Status so the parent's
// notice reads "finished (error: …)".
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

// transcript returns the child session's messages.jsonl from the runs store,
// or "" when unavailable. Works both while the job is active and after it
// finishes (the job is retained in m.jobs with its childID intact).
func (m *jobManager) transcript(id string) string {
	m.mu.Lock()
	var childID string
	if j := m.jobs[id]; j != nil {
		childID = j.childID
	}
	m.mu.Unlock()
	if childID == "" || m.rt == nil || m.rt.store == nil {
		return ""
	}
	return m.rt.store.Transcript(childID)
}

// parentName returns the session's registry name, or "" for a nil parent.
func parentName(s *Session) string {
	if s == nil {
		return ""
	}
	return s.name
}

// formatJobList renders all jobs as a compact listing for the task_list tool.
// Format: one line per job with id, type, status, and depth; running first.
func (m *jobManager) formatJobList() string {
	jobs := m.list() // already sorted: running first, then most-recently-done first
	if len(jobs) == 0 {
		return "no background tasks"
	}
	var b strings.Builder
	b.WriteString("background tasks:\n")
	for _, j := range jobs {
		kind := "command"
		if j.Kind == JobSubagent {
			kind = "@" + j.Agent
			if j.Agent == "" {
				kind = "subagent"
			}
		}
		fmt.Fprintf(&b, "  %s  %s  %s  depth=%d", j.ID, kind, jobStatusLabel(j.Done, j.Exit, j.Error), j.Depth)
		if j.Kind == JobSubagent && j.Cmd != "" {
			fmt.Fprintf(&b, "  — %s", j.Cmd)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderTranscriptText converts a messages.jsonl blob (one llm.Message JSON
// record per line) into human-readable plain text for the task_status tool.
// System and unparseable lines are skipped. Tool-call messages list the called
// tool name; tool-result messages show a one-line label so the model knows a
// result arrived without the full output; assistant and user text is emitted as
// "role: content".
func renderTranscriptText(raw string) string {
	var b strings.Builder
	for _, msg := range runs.ParseMessages(raw) {
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

// jobStatusCap caps formatJobStatus's total output (bytes) so a huge job
// result can't flood the model's context.
const jobStatusCap = 4000

// formatJobStatus renders one job's full status and a truncated result for the
// task_status tool.
func (m *jobManager) formatJobStatus(id string) string {
	// Copy the completion fields under the lock: finishers write them under
	// m.mu, so reading them off j after Unlock would race.
	m.mu.Lock()
	j := m.jobs[id]
	var (
		jKind    JobKind
		depth    int
		finished bool
		exit     *int
		summary  string
		errText  string
	)
	if j != nil {
		jKind, depth = j.kind, j.depth
		finished, exit = j.finished, j.exit
		summary, errText = j.summary, j.errText
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Sprintf("no such task %q", id)
	}

	kind := "command"
	if jKind == JobSubagent {
		kind = "subagent"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "task %s: %s (%s, depth %d)\n", id, jobStatusLabel(finished, exit, errText), kind, depth)

	if jKind == JobSubagent {
		if errText != "" {
			fmt.Fprintf(&b, "error: %s\n", strutil.Truncate(errText, 500))
		}
		if summary != "" {
			fmt.Fprintf(&b, "summary: %s\n", strutil.Truncate(summary, 2000))
		}
		// Prefer the on-disk transcript once the run ends; while it's still
		// running that file doesn't exist yet, so fall back to the live in-memory
		// buffer so the model sees progress (matching the :background modal).
		// Render the transcript as readable text; the live buffer is already readable.
		rawTranscript := m.transcript(id)
		body, label := renderTranscriptText(rawTranscript), "transcript"
		if rawTranscript == "" {
			body, label = m.output(id), "progress"
		}
		appendCappedTail(&b, label, body)
	} else {
		appendCappedTail(&b, "output", m.output(id))
	}
	return strings.TrimRight(b.String(), "\n")
}

// jobStatusLabel renders a job's one-word status for task_list/task_status:
// "running", "done", "error" (a failed subagent), or "error(exit N)" (a
// command that exited non-zero).
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

// appendCappedTail appends body under a "label:" header, spending at most the
// budget left before jobStatusCap. When body doesn't fit it keeps a rune-safe
// tail and adds a truncation marker; when the header lines alone have already
// used the budget it appends nothing.
func appendCappedTail(b *strings.Builder, label, body string) {
	if body == "" {
		return
	}
	// Reserve room for the header + truncation-marker lines so the tail budget
	// can never go negative (strutil.Tail returns "" for a non-positive budget).
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
