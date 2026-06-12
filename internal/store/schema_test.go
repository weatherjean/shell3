package store

import (
	"testing"
)

func TestMigrate_CreatesNewSchema(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Every new table/column must exist.
	checks := []string{
		`SELECT id, started_at, ended_at, summary, parent_session_id, pid, sock, status FROM sessions LIMIT 0`,
		`SELECT session_id, seq, role, content, tool_calls_json, tool_call_id, name, created_at FROM messages LIMIT 0`,
		`SELECT session_id, seq, payload_json, created_at FROM inbox LIMIT 0`,
	}
	for _, q := range checks {
		if _, err := st.db.Exec(q); err != nil {
			t.Errorf("schema missing for %q: %v", q, err)
		}
	}
}
