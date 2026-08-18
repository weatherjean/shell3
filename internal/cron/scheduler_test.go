//go:build unix

package cron

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

type fakeDispatcher struct {
	mu       sync.Mutex
	calls    []shell3.CronJob
	lastOpts shell3.DispatchOpts
}

func (f *fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, shell3.CronJob{Agent: agent, Prompt: prompt, WorkDir: opts.WorkDir, Name: opts.Description, Direct: opts.Direct})
	f.lastOpts = opts
	return "subX", nil
}
func (f *fakeDispatcher) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// fakeToolRunner stands in for a kit tool call: no shell, no model, just a
// scripted result/error so the scheduler's tool branch is testable alone.
type fakeToolRunner struct {
	mu      sync.Mutex
	calls   []string
	workDir string
	result  string
	err     error
}

func (f *fakeToolRunner) RunTool(_ context.Context, name, workDir string, _ map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	f.workDir = workDir
	return f.result, f.err
}

func TestScheduler_ToolJobSkipsDispatch(t *testing.T) {
	fd, ft := &fakeDispatcher{}, &fakeToolRunner{result: "3 rows updated"}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent"}}
	s, err := New(fd, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if fd.count() != 0 {
		t.Fatalf("a tool job must not dispatch an agent; dispatches=%d", fd.count())
	}
	if len(ft.calls) != 1 || ft.calls[0] != "sync-notion-recent" {
		t.Fatalf("tool calls = %v", ft.calls)
	}
}

func TestScheduler_ToolJobRecordsOutcome(t *testing.T) {
	ft := &fakeToolRunner{err: errors.New("notion 502")}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent"}}
	s, _ := New(&fakeDispatcher{}, ft, jobs)
	s.fire(jobs[0])
	got := s.Jobs()[0]
	if got.LastOK {
		t.Fatal("a failing tool must record LastOK=false")
	}
	if !strings.Contains(got.LastErr, "notion 502") {
		t.Fatalf("LastErr = %q", got.LastErr)
	}
	// LastRun is set on the same fire (fireAgent has always set it; a tool
	// job must not be a second-class citizen in /cron and /status).
	if got.LastRun == "" {
		t.Fatal("a tool job must record LastRun like an agent job does")
	}
	if got.Runs != 1 || got.Failures != 1 {
		t.Fatalf("Runs=%d Failures=%d, want 1 and 1", got.Runs, got.Failures)
	}
}

// TestScheduler_ToolJobEmptyResultStaysSilent pins the documented promise (see
// AGENTS.md, docs/kits.md): a tool that prints nothing on an idempotent
// no-op must not post an empty "⏰ job: " message every tick.
func TestScheduler_ToolJobEmptyResultStaysSilent(t *testing.T) {
	ft := &fakeToolRunner{result: ""}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent"}}
	s, err := New(&fakeDispatcher{}, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}
	var posted []shell3.CompletionPost
	s.SetPost(func(p shell3.CompletionPost) { posted = append(posted, p) })
	s.fire(jobs[0])
	if len(posted) != 0 {
		t.Fatalf("an empty result must stay silent, posted = %v", posted)
	}
}

// TestScheduler_ToolJobPostsFailure pins that a tool job's own failure — not
// just its success — reaches the post callback (fireAgent's floor-post
// equivalent, but posted directly since there is no dispatch to route it).
func TestScheduler_ToolJobPostsFailure(t *testing.T) {
	ft := &fakeToolRunner{err: errors.New("notion 502")}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent"}}
	s, err := New(&fakeDispatcher{}, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}
	var posted []shell3.CompletionPost
	s.SetPost(func(p shell3.CompletionPost) { posted = append(posted, p) })
	s.fire(jobs[0])
	if len(posted) != 1 || !strings.Contains(posted[0].Text, "⚠️") || !strings.Contains(posted[0].Text, "notion 502") {
		t.Fatalf("posted = %+v, want a ⚠️ failure post naming the error", posted)
	}
	// CronJob must stay EMPTY on a failure post: PostCompletion tests
	// CronJob != "" before it tests "is this already a failure", so a set
	// CronJob would wrap this already-self-describing "⚠️ job failed: …"
	// text in a second "⏰ job: " prefix. Text alone carries the job name.
	if posted[0].CronJob != "" {
		t.Fatalf("posted CronJob = %q, want empty (Text is already self-describing; a set CronJob double-prefixes at the host)", posted[0].CronJob)
	}
}

// TestScheduler_ToolJobHonoursWorkDir pins that a tool job's workdir:
// frontmatter reaches the tool call, exactly like a prompt job's does.
func TestScheduler_ToolJobHonoursWorkDir(t *testing.T) {
	ft := &fakeToolRunner{result: "ok"}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent", WorkDir: "/srv/notion"}}
	s, err := New(&fakeDispatcher{}, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if ft.workDir != "/srv/notion" {
		t.Fatalf("workDir = %q, want /srv/notion", ft.workDir)
	}
}

// TestScheduler_ToolJobPostsResult pins that a non-empty, non-NO_REPLY result
// reaches the post callback, and that a NO_REPLY result stays silent — the
// whole point of scheduling an idempotent tool every few minutes.
func TestScheduler_ToolJobPostsResult(t *testing.T) {
	ft := &fakeToolRunner{result: "3 rows updated"}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "sync-notion-recent"}}
	s, err := New(&fakeDispatcher{}, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}
	var posted []shell3.CompletionPost
	s.SetPost(func(p shell3.CompletionPost) { posted = append(posted, p) })
	s.fire(jobs[0])
	if len(posted) != 1 || !strings.Contains(posted[0].Text, "3 rows updated") {
		t.Fatalf("posted = %+v, want the tool result", posted)
	}
	// Text must NOT re-embed the job name: the host adds exactly one "⏰
	// <job>:" prefix from CronJob, so Text carrying it too would double it.
	if strings.Contains(posted[0].Text, "sync") {
		t.Fatalf("posted Text = %q, must not embed the job name (CronJob carries it)", posted[0].Text)
	}
	if posted[0].CronJob != "sync" {
		t.Fatalf("posted CronJob = %q, want the job name", posted[0].CronJob)
	}

	ft.result = "NO_REPLY"
	posted = nil
	s.fire(jobs[0])
	if len(posted) != 0 {
		t.Fatalf("NO_REPLY result must stay silent, posted = %v", posted)
	}
}

func TestScheduler_FireDispatches(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1s", Agent: "explorer", Prompt: "go", Direct: true}}
	s, err := New(fd, nil, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if fd.count() != 1 {
		t.Fatalf("want 1 dispatch, got %d", fd.count())
	}
	got := fd.calls[0]
	if got.Agent != "explorer" || got.Prompt != "go" || got.Name != "cron:j1" || !got.Direct {
		t.Fatalf("bad dispatch args: %+v", got)
	}
	// The dispatch carries the cron job name so the runtime routes ⏰ posts
	// and the ownerless wake path.
	if fd.lastOpts.CronJob != "j1" {
		t.Fatalf("CronJob = %q, want j1", fd.lastOpts.CronJob)
	}
	js := s.Jobs()
	if len(js) != 1 || js[0].Name != "j1" || js[0].LastSubID != "subX" || !js[0].Direct {
		t.Fatalf("bad job status: %+v", js)
	}
}

// waitFor polls cond until it returns true or a 1s deadline passes, failing
// the test on timeout. Run() fires off its own goroutine (see the doc
// comment on Scheduler.Run), so its effects land asynchronously.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within 1s")
}

// TestScheduler_Run covers the manual-trigger path (e.g. the /run command):
// Run fires exactly the named job and returns an error for an unknown name
// without dispatching anything. Run returns before the fire completes (see
// its doc comment — /run must not block the bot's update loop), so
// assertions on its effect wait rather than checking immediately.
func TestScheduler_Run(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{
		{Name: "nightly", Schedule: "@every 1h", Agent: "explorer", Prompt: "go"},
		{Name: "weekly", Schedule: "@every 1h", Agent: "explorer", Prompt: "go"},
	}
	s, err := New(fd, nil, jobs)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Run("nightly"); err != nil {
		t.Fatalf("Run(nightly): %v", err)
	}
	waitFor(t, func() bool { return fd.count() == 1 })
	if got := fd.calls[0].Name; got != "cron:nightly" {
		t.Errorf("dispatched label = %q, want cron:nightly", got)
	}

	if err := s.Run("nope"); err == nil {
		t.Fatal("Run(nope): want error for unknown job name")
	}
	// An unknown name is rejected synchronously (Run returns the error
	// before spawning anything), so no wait is needed for the negative case.
	if fd.count() != 1 {
		t.Fatalf("unknown-name Run fired a dispatch: count=%d", fd.count())
	}
}

// TestScheduler_RunIsAsync pins that Run returns before the job completes —
// the whole point for a tool job, which can block for up to toolJobTimeout.
// A caller on a single serialized loop (the bot's /run handler) must never
// be blocked by a fire.
func TestScheduler_RunIsAsync(t *testing.T) {
	release := make(chan struct{})
	ft := &blockingToolRunner{release: release}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1h", Tool: "sync-notion-recent"}}
	s, err := New(&fakeDispatcher{}, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		if err := s.Run("sync"); err != nil {
			t.Error(err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly — it must not block on the fire")
	}
	close(release) // let the blocked tool call finish, so the goroutine doesn't leak
}

// blockingToolRunner blocks RunTool until release closes, standing in for a
// slow tool call so TestScheduler_RunIsAsync can prove Run doesn't wait on it.
type blockingToolRunner struct{ release chan struct{} }

func (b *blockingToolRunner) RunTool(ctx context.Context, name, workDir string, args map[string]any) (string, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return "", nil
}

func TestScheduler_BadScheduleRejected(t *testing.T) {
	if _, err := New(&fakeDispatcher{}, nil, []shell3.CronJob{{Name: "x", Schedule: "not a cron", Agent: "a"}}); err == nil {
		t.Fatal("expected error for malformed schedule")
	}
}

func TestScheduler_StartStopClean(t *testing.T) {
	s, _ := New(&fakeDispatcher{}, nil, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	s.Start()
	time.Sleep(20 * time.Millisecond)
	s.Stop() // must not hang
}
