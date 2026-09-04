package shell3

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestCommandCompletionsUseFilesystemInbox(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	if _, err := rt.jobs.startCommand(nil, "printf complete", t.TempDir(), []string{"printf", "complete"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.jobs.startCommand(nil, "exit seven", t.TempDir(), []string{"sh", "-c", "printf failed; exit 7"}, nil); err != nil {
		t.Fatal(err)
	}
	rt.jobs.wait()

	notices, total, err := rt.mainInbox().List("main", inbox.StatusPending, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(notices) != 2 {
		t.Fatalf("notices total=%d page=%+v", total, notices)
	}
	byEvent := make(map[string]inbox.Message, len(notices))
	for _, notice := range notices {
		byEvent[notice.Message.Event] = notice.Message
		if notice.Message.To != "main" || notice.Message.Trust != "machine" {
			t.Fatalf("notice identity = %+v", notice.Message)
		}
	}
	if got := byEvent["bash_bg.completed"]; !strings.Contains(got.Body, "complete") || got.Correlation == "" {
		t.Fatalf("completed notice = %+v", got)
	}
	if got := byEvent["bash_bg.failed"]; !strings.Contains(got.Body, "exit 7") || !strings.Contains(got.Body, "failed") {
		t.Fatalf("failed notice = %+v", got)
	}
}

func TestRecoverBackgroundJobsPersistsFailureBeforeDeletingMarker(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	id, err := rt.store.BackgroundJobPut(runs.BackgroundJob{
		PID: 1<<30 - 1, JobID: "bg99", Title: "long task",
		LogPath: "/tmp/bg99.log", StartedAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("zero marker id")
	}

	if got := rt.RecoverBackgroundJobs(); got != 1 {
		t.Fatalf("recovered = %d, want 1", got)
	}
	markers, err := rt.store.BackgroundJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("markers remain: %+v", markers)
	}
	notices, total, err := rt.mainInbox().List("main", inbox.StatusPending, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(notices) != 1 {
		t.Fatalf("recovery notices total=%d page=%+v", total, notices)
	}
	msg := notices[0].Message
	if msg.Event != "bash_bg.failed" || msg.Correlation != "bg99" || !strings.Contains(msg.Body, "final result was lost") {
		t.Fatalf("recovery notice = %+v", msg)
	}
}

func TestRecoverBackgroundJobsLeavesCurrentProcessMarker(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("unused"))
	if _, err := rt.store.BackgroundJobPut(runs.BackgroundJob{
		PID: os.Getpid(), JobID: "bg-live", Title: "still running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := rt.RecoverBackgroundJobs(); got != 0 {
		t.Fatalf("recovered current-process marker = %d", got)
	}
	markers, err := rt.store.BackgroundJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 || markers[0].JobID != "bg-live" {
		t.Fatalf("live marker changed: %+v", markers)
	}
	_, total, err := rt.mainInbox().List("main", inbox.StatusPending, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("live marker produced %d notices", total)
	}
}
