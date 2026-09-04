package runs

import (
	"testing"
	"time"
)

func TestBackgroundJobPutLoadDelete(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	started := time.Date(2026, 9, 4, 8, 30, 0, 0, time.UTC)
	id1, err := st.BackgroundJobPut(BackgroundJob{PID: 42, JobID: "bg1", Title: "make test", OwnerID: "sess1", LogPath: "/tmp/bg1.log", StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := st.BackgroundJobPut(BackgroundJob{PID: 43, JobID: "bg2", Title: "sleep 1", StartedAt: started.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatalf("ids must be distinct, both %d", id1)
	}

	rows, err := st.BackgroundJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].PID != 42 || rows[0].JobID != "bg1" || rows[0].OwnerID != "sess1" || !rows[0].StartedAt.Equal(started) {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[1].PID != 43 || rows[1].JobID != "bg2" {
		t.Fatalf("row 1 = %+v", rows[1])
	}

	if err := st.BackgroundJobDelete(id1); err != nil {
		t.Fatal(err)
	}
	rows, err = st.BackgroundJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id2 {
		t.Fatalf("after delete want only id %d, got %+v", id2, rows)
	}
}

func TestBackgroundJobSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BackgroundJobPut(BackgroundJob{PID: 42, JobID: "bg1", Title: "work", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	rows, err := st2.BackgroundJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].JobID != "bg1" {
		t.Fatalf("want the row back after reopen, got %+v", rows)
	}
}
