package runs

import (
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSavePromptRoundTrip(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.SavePrompt("s1", 0, "you are an agent", now); err != nil {
		t.Fatal(err)
	}
	got := st.PromptsForSession("s1")
	if len(got) != 1 || got[0].Text != "you are an agent" || got[0].Seq != 0 {
		t.Fatalf("PromptsForSession = %+v", got)
	}
	if got[0].Hash == "" {
		t.Fatal("a stored prompt needs its content address")
	}
	if got[0].TS.IsZero() {
		t.Fatal("a stored prompt needs its timestamp")
	}
}

func TestSavePromptSkipsUnchanged(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	for seq := 0; seq < 5; seq++ {
		if err := st.SavePrompt("s1", seq, "same body", now); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.PromptsForSession("s1"); len(got) != 1 {
		t.Fatalf("%d rows for an unchanged prompt, want 1", len(got))
	}

	if err := st.SavePrompt("s1", 5, "edited body", now); err != nil {
		t.Fatal(err)
	}
	got := st.PromptsForSession("s1")
	if len(got) != 2 || got[1].Text != "edited body" || got[1].Seq != 5 {
		t.Fatalf("a changed prompt must be recorded at its seq: %+v", got)
	}
}

func TestSavePromptDedupesAcrossSessions(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.SavePrompt("s1", 0, "shared body", now); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePrompt("s2", 0, "shared body", now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM prompts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d prompt bodies stored, want 1 — content addressing is the whole point", n)
	}
	if len(st.PromptsForSession("s1")) != 1 || len(st.PromptsForSession("s2")) != 1 {
		t.Fatal("both sessions must still resolve their prompt")
	}
}

func TestSavePromptIsNotSearchable(t *testing.T) {
	st := openTestStore(t)
	if err := st.SavePrompt("s1", 0, "gravitational singularity", time.Now()); err != nil {
		t.Fatal(err)
	}
	hits, err := st.Search("gravitational", 10)
	if err != nil && !strings.Contains(err.Error(), "no such") {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("prompt text leaked into history search: %+v", hits)
	}
}

func TestSweepCollectsOrphanPrompts(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.SavePrompt("s1", 0, "only user", now); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePrompt("s2", 0, "shared", now); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePrompt("s3", 0, "shared", now); err != nil {
		t.Fatal(err)
	}
	if err := st.deleteSessions([]string{"s1", "s2"}); err != nil {
		t.Fatal(err)
	}
	var bodies int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM prompts`).Scan(&bodies); err != nil {
		t.Fatal(err)
	}
	if bodies != 1 {
		t.Fatalf("%d bodies after deleting s1 and s2, want 1 (s3 still references \"shared\")", bodies)
	}
	if len(st.PromptsForSession("s1")) != 0 {
		t.Fatal("a deleted session must keep no prompt references")
	}
	if len(st.PromptsForSession("s3")) != 1 {
		t.Fatal("a surviving session must keep its prompt")
	}
}

func TestSavePromptNilStore(t *testing.T) {
	var st *Store
	if err := st.SavePrompt("s1", 0, "body", time.Now()); err != nil {
		t.Fatalf("nil store should no-op, got %v", err)
	}
	if got := st.PromptsForSession("s1"); len(got) != 0 {
		t.Fatalf("nil store returned %+v", got)
	}
}
