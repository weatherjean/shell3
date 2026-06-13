package store

import "testing"

func TestStartSession_RecordsProjectAndWorkdir(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, err := st.StartSession("proj-abc", "/tmp/work", "")
	if err != nil {
		t.Fatal(err)
	}
	var uuid, wd string
	if err := st.db.QueryRow(
		`SELECT project_uuid, workdir FROM sessions WHERE id=?`, id).Scan(&uuid, &wd); err != nil {
		t.Fatal(err)
	}
	if uuid != "proj-abc" || wd != "/tmp/work" {
		t.Fatalf("got (%q,%q), want (proj-abc,/tmp/work)", uuid, wd)
	}
}

func TestSessionConfigPath_RoundTrip(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()

	id, err := st.StartSession("proj", "/x", "/x/.shell3/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionConfigPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/x/.shell3/shell3.lua" {
		t.Fatalf("config path = %q, want /x/.shell3/shell3.lua", got)
	}

	bare, err := st.StartSession("proj", "/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.SessionConfigPath(bare); err != nil || got != "" {
		t.Fatalf("bare config path = %q,%v; want \"\",nil", got, err)
	}
}

func TestSession_PointerAndLiveness(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()

	parent, _ := st.StartSession("", "", "")
	child, err := st.StartSessionWithParent(parent, "", "", "")
	if err != nil {
		t.Fatalf("start child: %v", err)
	}

	gotParent, err := st.ParentSessionID(child)
	if err != nil || gotParent != parent {
		t.Fatalf("parent pointer = %d,%v; want %d", gotParent, err, parent)
	}

	if err := st.SetLiveness(parent, 4242, "/tmp/p.sock", "live"); err != nil {
		t.Fatalf("set liveness: %v", err)
	}
	pid, sock, status, err := st.Liveness(parent)
	if err != nil || pid != 4242 || sock != "/tmp/p.sock" || status != "live" {
		t.Fatalf("liveness = %d,%q,%q,%v", pid, sock, status, err)
	}
}

func TestSession_ReviveClaim_SingleWinner(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession("", "", "")
	_ = st.SetLiveness(id, 0, "", "dormant")

	won1, err := st.ClaimRevive(id, 0)
	if err != nil {
		t.Fatalf("claim1: %v", err)
	}
	won2, _ := st.ClaimRevive(id, 0)
	if !won1 || won2 {
		t.Fatalf("expected exactly one winner; won1=%v won2=%v", won1, won2)
	}
}

// TestSession_ReviveClaim_ReclaimsCrashedLiveParent: a parent that registered
// "live" then died without cleanup (kill -9) stays "live" with a stale pid.
// A reporter that confirms the pid is dead must be able to reclaim it, else
// every report to it strands (ClaimRevive used to fire only on "dormant").
func TestSession_ReviveClaim_ReclaimsCrashedLiveParent(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession("", "", "")
	const deadPID = 2147483646 // not a running process
	_ = st.SetLiveness(id, deadPID, "/tmp/p.sock", "live")

	won, err := st.ClaimRevive(id, deadPID)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if !won {
		t.Fatal("expected to reclaim a crashed-but-live parent")
	}
	if again, _ := st.ClaimRevive(id, deadPID); again {
		t.Fatal("reclaim must elect a single winner (second claim won)")
	}
}

// TestSession_ReviveClaim_LeavesHealthyLiveParent: a "live" parent must NOT be
// reclaimed when the reporter has no dead pid to offer (deadPID 0) — a healthy
// parent is reached over its socket, never double-revived.
func TestSession_ReviveClaim_LeavesHealthyLiveParent(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession("", "", "")
	_ = st.SetLiveness(id, 4242, "/tmp/p.sock", "live")

	if won, err := st.ClaimRevive(id, 0); err != nil || won {
		t.Fatalf("must not reclaim a live parent with deadPID 0; won=%v err=%v", won, err)
	}
	if won, _ := st.ClaimRevive(id, 999999); won {
		t.Fatal("must not reclaim a live parent whose pid does not match deadPID")
	}
}

func TestListProjects_DistinctWithLastActivity(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	if _, err := st.StartSession("projA", "/a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession("projA", "/a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession("projB", "/b", ""); err != nil {
		t.Fatal(err)
	}
	ps, err := st.ListProjects(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d projects, want 2 distinct", len(ps))
	}
	// projA has 2 sessions
	var foundA bool
	for _, p := range ps {
		if p.UUID == "projA" {
			foundA = true
			if p.SessionCount != 2 {
				t.Fatalf("projA SessionCount=%d, want 2", p.SessionCount)
			}
			if p.Workdir != "/a" {
				t.Fatalf("projA Workdir=%q, want /a", p.Workdir)
			}
		}
	}
	if !foundA {
		t.Fatalf("projA missing from %+v", ps)
	}
}

func TestSession_Inbox_AppendDrain(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession("", "", "")

	_ = st.AppendInbox(id, []byte(`{"kind":"agent_done","id":"a1"}`))
	_ = st.AppendInbox(id, []byte(`{"kind":"agent_done","id":"a2"}`))

	got, err := st.DrainInbox(id)
	if err != nil || len(got) != 2 {
		t.Fatalf("drain = %d items, %v; want 2", len(got), err)
	}
	// Draining again yields nothing — drain is destructive.
	again, _ := st.DrainInbox(id)
	if len(again) != 0 {
		t.Fatalf("second drain = %d; want 0", len(again))
	}
}
