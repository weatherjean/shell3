//go:build unix

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// A finished job's elapsed time is how long it TOOK. Measuring to now instead
// made every completed job appear to still be running up its clock.
func TestDescribeJobMeasuresFinishedWorkToItsEnd(t *testing.T) {
	started := time.Now().Add(-2 * time.Hour)
	exit := 0
	job := describeJob(shell3.JobInfo{
		ID: "bg1", Cmd: "sleep 5", Kind: shell3.JobCommand,
		StartedAt: started, EndedAt: started.Add(5 * time.Second),
		Done: true, Exit: &exit,
	})

	if job.Elapsed != 5 {
		t.Errorf("elapsed = %ds, want 5s (the time it ran, not the time since)", job.Elapsed)
	}
	if job.Status != "done" {
		t.Errorf("status = %q, want done", job.Status)
	}
	if job.Exit == nil || *job.Exit != 0 {
		t.Errorf("exit = %v, want 0", job.Exit)
	}
}

func TestDescribeJobRunningMeasuresToNow(t *testing.T) {
	job := describeJob(shell3.JobInfo{
		ID: "bg1", Kind: shell3.JobCommand,
		StartedAt: time.Now().Add(-30 * time.Second),
	})
	if job.Status != "running" {
		t.Errorf("status = %q, want running", job.Status)
	}
	if job.Elapsed < 29 || job.Elapsed > 32 {
		t.Errorf("elapsed = %ds, want about 30s", job.Elapsed)
	}
}

// A job carrying an error reads as failed even when the runtime marked it done.
func TestDescribeJobFailureBeatsDone(t *testing.T) {
	job := describeJob(shell3.JobInfo{
		ID: "sub1", Agent: "researcher", Kind: shell3.JobSubagent,
		StartedAt: time.Now(), Done: true, Error: "model refused",
	})
	if job.Status != "failed" {
		t.Errorf("status = %q, want failed", job.Status)
	}
	if job.Kind != "subagent" || job.Label != "researcher" {
		t.Errorf("job = %+v, want a subagent labelled by its agent", job)
	}
	if job.Error != "model refused" {
		t.Errorf("error text was dropped: %+v", job)
	}
}

func TestCronListsDeclaredJobsWithoutAScheduler(t *testing.T) {
	srv := newTestServer(t, "ok")

	rec := httptest.NewRecorder()
	srv.handleCron(rec, httptest.NewRequest(http.MethodGet, "/api/cron", nil))

	got := decode[struct {
		Jobs  []cronJobResp `json:"jobs"`
		Armed bool          `json:"armed"`
	}](t, rec.Body.String())

	if got.Armed {
		t.Error("no scheduler is armed in the test runtime")
	}
	if got.Jobs == nil {
		t.Error("jobs must marshal as [] rather than null")
	}
}

// Running a cron job needs a scheduler; without one the request must say so
// rather than silently doing nothing.
func TestCronRunWithoutASchedulerConflicts(t *testing.T) {
	srv := newTestServer(t, "ok")

	rec := httptest.NewRecorder()
	srv.handleCronRun(rec, httptest.NewRequest(http.MethodPost, "/api/cron/x/run", nil), "x")
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestCronRunRejectsNonPost(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handleCronRun(rec, httptest.NewRequest(http.MethodGet, "/api/cron/x/run", nil), "x")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestSetCronSourceWiresBothListAndRun(t *testing.T) {
	srv := newTestServer(t, "ok")
	if srv.cronSource != nil || srv.runCron != nil {
		t.Fatal("a fresh server has no scheduler")
	}

	sched, err := cron.New(nil, []shell3.CronJob{
		{Name: "nightly", Schedule: "@daily", Agent: "explorer", Prompt: "check things"},
	})
	if err != nil {
		t.Skipf("scheduler unavailable: %v", err)
	}
	srv.SetCronSource(sched)

	if srv.cronSource == nil || srv.runCron == nil {
		t.Fatal("SetCronSource must wire the listing AND the manual fire")
	}

	// A reload that removed the last cron/ file disarms with nil.
	srv.SetCronSource(nil)
	if src, run := srv.cronFuncs(); src != nil || run != nil {
		t.Fatal("SetCronSource(nil) must disarm both")
	}
	srv.SetCronSource(sched)

	rec := httptest.NewRecorder()
	srv.handleCron(rec, httptest.NewRequest(http.MethodGet, "/api/cron", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "nightly") || !strings.Contains(body, "check things") {
		t.Errorf("the job's name and prompt should both be served: %s", body)
	}
}

// Runs must marshal as [] when the store is empty; the browser iterates them.
func TestRunsListIsNeverNull(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handleRuns(rec, httptest.NewRequest(http.MethodGet, "/api/runs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Errorf("runs JSON contains null: %s", rec.Body.String())
	}
}

func TestRunTranscriptRejectsUnknownRun(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handleRunTranscript(rec, "no-such-run")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRouteJobsSplitsDetailFromCancel(t *testing.T) {
	srv := newTestServer(t, "ok")

	// Detail for an unknown job is a 404, not a cancel.
	rec := httptest.NewRecorder()
	srv.routeJobs(rec, httptest.NewRequest(http.MethodGet, "/api/jobs/bg9", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("detail status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.routeJobs(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/bg9/cancel", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("cancel status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.routeJobs(rec, httptest.NewRequest(http.MethodGet, "/api/jobs/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id status = %d, want 400", rec.Code)
	}
}

// The media dir is flat, so any separator in a request name is an escape
// attempt and must be refused rather than cleaned.
func TestMediaFileRefusesPathTricks(t *testing.T) {
	srv := newTestServer(t, "ok")
	for _, name := range []string{"../.env", "a/b.png", "..%2F.env", "sub\\file.png", ".."} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/media/x", nil)
		req.URL.Path = "/api/media/" + name
		srv.handleMediaFile(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("name %q returned %d, want 404", name, rec.Code)
		}
	}
}

func TestMediaListIsNeverNull(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handleMediaList(rec, httptest.NewRequest(http.MethodGet, "/api/media", nil))
	if strings.Contains(rec.Body.String(), "null") {
		t.Errorf("media JSON contains null: %s", rec.Body.String())
	}
}

// A compaction discards conversation history; the user learns about it.
func TestCompactionReachesTheBell(t *testing.T) {
	srv := newTestServer(t, "ok")
	events, cancel := srv.hub.subscribe()
	defer cancel()

	srv.turnNotice("compacted", "summarized 40 messages into 1")

	select {
	case ev := <-events:
		if ev.Name != "notification" {
			t.Fatalf("event = %q, want notification", ev.Name)
		}
		if !strings.Contains(string(ev.Data), "summarized 40 messages") {
			t.Errorf("notification lost the detail: %s", ev.Data)
		}
	default:
		t.Fatal("a compaction published nothing")
	}
}

// Retries are transient and often arrive several in a row; they belong in the
// log, not in the user's notifications.
func TestRetryDoesNotReachTheBell(t *testing.T) {
	srv := newTestServer(t, "ok")
	events, cancel := srv.hub.subscribe()
	defer cancel()

	srv.turnNotice("retry", "attempt 2")

	select {
	case ev := <-events:
		t.Errorf("a retry published %q; it should only be logged", ev.Name)
	default:
	}
}

// Notifications are replayed to a browser that connects later, so a closed tab
// does not lose the evening's background work.
func TestNotificationsAreReplayable(t *testing.T) {
	srv := newTestServer(t, "ok")

	srv.PostCompletion("", "", "first")
	srv.PostCompletion("", "", "second")

	recent := srv.recentNotices()
	if len(recent) != 2 {
		t.Fatalf("buffer holds %d, want 2", len(recent))
	}
	if recent[0].Body != "first" || recent[1].Body != "second" {
		t.Errorf("buffer out of order: %+v", recent)
	}
	if recent[0].ID == recent[1].ID {
		t.Error("each notification needs its own id so the client can dedupe")
	}
}

func TestNotificationBufferIsBounded(t *testing.T) {
	srv := newTestServer(t, "ok")
	for i := 0; i < recentNotifications+25; i++ {
		srv.PostCompletion("", "", "note")
	}
	if got := len(srv.recentNotices()); got != recentNotifications {
		t.Errorf("buffer holds %d, want it capped at %d", got, recentNotifications)
	}
}

// Usage is reported per turn; a turn that reported nothing must not blank the
// previous reading.
func TestUsageKeepsTheLastRealReading(t *testing.T) {
	srv := newTestServer(t, "ok")

	srv.recordUsage(turnUsage{Prompt: 100, Completion: 20, Total: 120})
	srv.recordUsage(turnUsage{}) // a cancelled turn reports nothing

	got := srv.lastUsage()
	if got == nil || got.Total != 120 {
		t.Errorf("usage = %+v, want the last real reading", got)
	}
}
