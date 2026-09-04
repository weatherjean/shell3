package runs

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestCurrentSessionRecordLookup(t *testing.T) {
	st, _ := Open(t.TempDir())
	if err := st.SetCurrentSession("main", "sess-a"); err != nil {
		t.Fatal(err)
	}
	if got, ok := st.CurrentSession("main"); !ok || got != "sess-a" {
		t.Fatalf("CurrentSession = %q, %v", got, ok)
	}
	if _, ok := st.CurrentSession("other"); ok {
		t.Fatal("surface isolation broken")
	}
	if err := st.SetCurrentSession("main", "sess-b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.CurrentSession("main"); got != "sess-b" {
		t.Fatalf("overwrite failed: %q", got)
	}
}

func TestReopenPersists(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	id, _ := st.NewSession()
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hello there"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentSession("main", id); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := st2.LoadMessages(id)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello there" {
		t.Fatalf("messages lost across reopen: %v %v", msgs, err)
	}
	if got, ok := st2.CurrentSession("main"); !ok || got != id {
		t.Fatalf("current-session marker lost across reopen: %q %v", got, ok)
	}
}
