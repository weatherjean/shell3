package jobstore

import (
	"os"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/store"
)

// Compile-time check that the adapter satisfies the bgjobs.Registry interface.
var _ bgjobs.Registry = (*Store)(nil)

func TestAddListClearRoundTrip(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := New(st)
	wd := "/tmp/workdir-x"
	// Use a LIVE pid so ListJobs' dead-pid prune does not drop the row.
	job := bgjobs.Job{
		ID: "bg_abc123", PID: os.Getpid(), Cmd: "sleep 1", Log: "/tmp/shell3/runs/bg_abc123.log",
		Workdir: wd, StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := a.Add(job); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := a.List(wd)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 job, got %d: %v", len(got), got)
	}
	g := got[0]
	if g.ID != job.ID || g.PID != job.PID || g.Cmd != job.Cmd || g.Log != job.Log || g.Workdir != job.Workdir {
		t.Fatalf("round-trip mismatch: got %+v want %+v", g, job)
	}

	// A different workdir is isolated.
	if other, err := a.List("/tmp/other"); err != nil || len(other) != 0 {
		t.Fatalf("other workdir should be empty, got %v err %v", other, err)
	}

	n, err := a.Clear(wd)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 1 {
		t.Fatalf("Clear removed %d, want 1", n)
	}
	if got, err := a.List(wd); err != nil || len(got) != 0 {
		t.Fatalf("after clear want empty, got %v err %v", got, err)
	}
}
