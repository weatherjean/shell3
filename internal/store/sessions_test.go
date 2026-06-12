package store

import "testing"

func TestSession_PointerAndLiveness(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()

	parent, _ := st.StartSession()
	child, err := st.StartSessionWithParent(parent)
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
	id, _ := st.StartSession()
	_ = st.SetLiveness(id, 0, "", "dormant")

	won1, err := st.ClaimRevive(id)
	if err != nil {
		t.Fatalf("claim1: %v", err)
	}
	won2, _ := st.ClaimRevive(id)
	if !won1 || won2 {
		t.Fatalf("expected exactly one winner; won1=%v won2=%v", won1, won2)
	}
}

func TestSession_Inbox_AppendDrain(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession()

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
