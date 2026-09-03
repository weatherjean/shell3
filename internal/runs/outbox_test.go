package runs

import "testing"

func TestOutboxPutLoadDelete(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id1, err := st.OutboxPut("event", `{"job":"bg1"}`)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := st.OutboxPut("running", `{"job":"sub1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatalf("ids must be distinct, both %d", id1)
	}

	rows, err := st.OutboxLoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Kind != "event" || rows[0].JSON != `{"job":"bg1"}` {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[1].Kind != "running" || rows[1].JSON != `{"job":"sub1"}` {
		t.Fatalf("row 1 = %+v", rows[1])
	}

	if err := st.OutboxDelete(id1); err != nil {
		t.Fatal(err)
	}
	rows, err = st.OutboxLoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id2 {
		t.Fatalf("after delete want only id %d, got %+v", id2, rows)
	}
}

func TestOutboxSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.OutboxPut("event", `{"job":"bg1"}`); err != nil {
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
	rows, err := st2.OutboxLoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].JSON != `{"job":"bg1"}` {
		t.Fatalf("want the row back after reopen, got %+v", rows)
	}
}
