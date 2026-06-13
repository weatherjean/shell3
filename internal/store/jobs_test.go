package store

import (
	"os"
	"testing"
)

func TestJobs_AddListPrunesDeadAndClear(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	live := os.Getpid()
	const dead = 2147483646
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.AddJob(Job{ID: "bg_live", PID: live, Cmd: "sleep 1", Workdir: "/w"}))
	must(st.AddJob(Job{ID: "bg_dead", PID: dead, Cmd: "echo hi", Workdir: "/w"}))
	must(st.AddJob(Job{ID: "bg_other", PID: live, Cmd: "x", Workdir: "/other"}))

	// List for /w prunes the dead entry and returns only the live one.
	jobs, err := st.ListJobs("/w", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "bg_live" {
		t.Fatalf("got %+v, want only bg_live", jobs)
	}
	// The dead row was pruned from the table.
	if n := jobCount(t, st); n != 2 { // bg_live + bg_other
		t.Fatalf("table has %d rows, want 2 (dead pruned)", n)
	}
	// Clear removes only /w's jobs, returns count cleared.
	n, err := st.ClearJobs("/w")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cleared %d, want 1", n)
	}
}

func jobCount(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
