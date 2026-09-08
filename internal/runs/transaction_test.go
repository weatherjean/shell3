package runs

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestHistoryTransitionRollsBackOnMessageFailure(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "append", true: "compact"}[compact], func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			id, err := st.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetCurrentSession("test", id); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(`CREATE TRIGGER reject_message BEFORE INSERT ON messages WHEN NEW.json LIKE '%reject-me%' BEGIN SELECT RAISE(FAIL, 'injected message failure'); END`); err != nil {
				t.Fatal(err)
			}
			messages := []llm.Message{{Role: llm.RoleUser, Content: "accepted"}, {Role: llm.RoleAssistant, Content: "reject-me"}}
			if compact {
				_, err = st.RollSession(id, messages[:1], messages)
			} else {
				err = st.AppendMessages(id, messages)
			}
			if err == nil {
				t.Fatal("expected injected failure")
			}
			got, err := st.LoadMessages(id)
			if err != nil || len(got) != 0 {
				t.Fatalf("partial history persisted: %v, %v", got, err)
			}
			if current, ok := st.CurrentSession("test"); !ok || current != id {
				t.Fatalf("marker changed: %q", current)
			}
			var count int
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("orphaned session: count=%d, %v", count, err)
			}
			if _, err := st.db.Exec(`DROP TRIGGER reject_message`); err != nil {
				t.Fatal(err)
			}
			if compact {
				newID, err := st.RollSession(id, messages[:1], messages)
				if err != nil {
					t.Fatal(err)
				}
				if current, _ := st.CurrentSession("test"); current != newID {
					t.Fatal("marker did not advance")
				}
				id = newID
			} else if err := st.AppendMessages(id, messages); err != nil {
				t.Fatal(err)
			}
			got, err = st.LoadMessages(id)
			if err != nil || len(got) != 2 {
				t.Fatalf("retry history = %v, %v", got, err)
			}
		})
	}
}
