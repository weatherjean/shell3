//go:build unix

package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

// openTestStore opens a runs store rooted at a temp dir, for tests that want
// to exercise the thread index against a real database.
func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fixedStore returns a resolver that always answers st, mimicking a runtime
// generation that never reloads.
func fixedStore(st *runs.Store) func() *runs.Store {
	return func() *runs.Store { return st }
}

func TestThreadIndexPersistsAcrossReopen(t *testing.T) {
	st := openTestStore(t)

	ti := newThreadIndex(fixedStore(st))
	ti.record("thread-a", "sess-1", "")
	ti.record("thread-b", "sess-2", "")

	// A fresh index against the same store simulates a process restart: the
	// in-memory map starts empty and is rebuilt entirely from the store.
	reopened := newThreadIndex(fixedStore(st))

	if got, ok := reopened.lookup("thread-a"); !ok || got != "sess-1" {
		t.Errorf("lookup(thread-a) = %q, %v; want sess-1, true", got, ok)
	}
	if got, ok := reopened.lookup("thread-b"); !ok || got != "sess-2" {
		t.Errorf("lookup(thread-b) = %q, %v; want sess-2, true", got, ok)
	}
	if _, ok := reopened.lookup("missing"); ok {
		t.Error("lookup of an unrecorded thread should miss")
	}
}

// A nil store degrades to memory-only: the index still works within the
// process, it just has nothing to reload from a restart.
func TestThreadIndexWorksWithNilStore(t *testing.T) {
	ti := newThreadIndex(nil)
	ti.record("thread-a", "sess-1", "hello")

	if got, ok := ti.lookup("thread-a"); !ok || got != "sess-1" {
		t.Errorf("lookup(thread-a) = %q, %v; want sess-1, true", got, ok)
	}
	if list := ti.list(); len(list) != 1 {
		t.Errorf("list has %d entries, want 1", len(list))
	}
}

func TestThreadIndexResolvesStorePerCall(t *testing.T) {
	st1 := openTestStore(t)
	st2 := openTestStore(t)

	calls := 0
	current := st1
	resolver := func() *runs.Store {
		calls++
		return current
	}

	ti := newThreadIndex(resolver)
	ti.record("thread-a", "sess-1", "")

	// Swap the generation, as a /reload would: subsequent writes must land on
	// the new store, never the closed one.
	current = st2
	ti.record("thread-b", "sess-2", "")

	if calls == 0 {
		t.Fatal("resolver was never called")
	}
	if _, ok := st1.ThreadLookup(webSurface, "thread-b"); ok {
		t.Error("a write after the generation swap should not land on the old store")
	}
	if got, ok := st2.ThreadLookup(webSurface, "thread-b"); !ok || got != "sess-2" {
		t.Errorf("st2.ThreadLookup(thread-b) = %q, %v; want sess-2, true", got, ok)
	}
}

// A dead thread — one whose session Sweep has removed — disappears once
// Sweep runs. There is no webui-specific pruning any more: the shared
// runs.Sweep already drops thread rows whose session id no longer exists,
// for every surface.
func TestPruneDeadThreadsViaSweep(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	liveID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}

	ti := newThreadIndex(fixedStore(st))
	ti.record("keep", liveID, "")
	ti.record("drop", "sess-never-existed", "")

	if _, _, err := runs.Sweep(root, 0, time.Now()); err != nil {
		t.Fatal(err)
	}

	reopened := newThreadIndex(fixedStore(st))
	if _, ok := reopened.lookup("drop"); ok {
		t.Error("the thread pointing at a nonexistent session should be gone")
	}
	if got, ok := reopened.lookup("keep"); !ok || got != liveID {
		t.Errorf("keep's thread should survive: got %q, %v", got, ok)
	}
}

// A rename must survive a restart, and must not lose the session mapping —
// a rename writes no session id of its own.
func TestThreadIndexRenameSurvivesReopen(t *testing.T) {
	st := openTestStore(t)

	ti := newThreadIndex(fixedStore(st))
	ti.record("t1", "sess-1", "")
	if _, ok := ti.rename("t1", "Deploy notes"); !ok {
		t.Fatal("rename should find the thread")
	}

	reopened := newThreadIndex(fixedStore(st))
	rec, ok := reopened.get("t1")
	if !ok || rec.Title != "Deploy notes" {
		t.Errorf("record = %+v, want the renamed thread", rec)
	}
	if got, ok := reopened.lookup("t1"); !ok || got != "sess-1" {
		t.Errorf("session mapping lost across rename: %q, %v", got, ok)
	}
}

// A deleted thread disappears from the listing and stays gone across a
// restart, even though the store keeps the tombstoned row.
func TestThreadIndexDeleteIsPermanent(t *testing.T) {
	st := openTestStore(t)

	ti := newThreadIndex(fixedStore(st))
	ti.record("gone", "sess-1", "")
	if !ti.remove("gone") {
		t.Fatal("remove should report success")
	}
	if len(ti.list()) != 0 {
		t.Error("a deleted thread should not appear in the listing")
	}

	reopened := newThreadIndex(fixedStore(st))
	if _, ok := reopened.get("gone"); ok {
		t.Error("a deleted thread came back after a restart")
	}
}

// The listing is newest-activity-first, which is the order the sidebar shows.
func TestThreadIndexListsNewestFirst(t *testing.T) {
	st := openTestStore(t)
	ti := newThreadIndex(fixedStore(st))

	ti.record("older", "sess-1", "")
	time.Sleep(1100 * time.Millisecond) // the stamp has second resolution
	ti.record("newer", "sess-2", "")

	list := ti.list()
	if len(list) != 2 || list[0].ID != "newer" {
		t.Errorf("list = %+v, want newer first", list)
	}
}

// A thread is named by what was first asked in it. The first preview wins:
// renaming the conversation on every message would make the list unstable.
func TestThreadPreviewIsSetOnceFromTheFirstMessage(t *testing.T) {
	st := openTestStore(t)
	ti := newThreadIndex(fixedStore(st))

	ti.record("t1", "sess-1", "plan the migration")
	ti.record("t1", "sess-1", "and now something else")

	rec, _ := ti.get("t1")
	if rec.Preview != "plan the migration" {
		t.Errorf("preview = %q, want the first message", rec.Preview)
	}

	reopened := newThreadIndex(fixedStore(st))
	if rec, _ := reopened.get("t1"); rec.Preview != "plan the migration" {
		t.Errorf("preview = %q after restart, want it preserved", rec.Preview)
	}
}

// A rename writes no preview of its own; it must not erase the one there.
func TestRenameKeepsThePreview(t *testing.T) {
	st := openTestStore(t)
	ti := newThreadIndex(fixedStore(st))

	ti.record("t1", "sess-1", "plan the migration")
	ti.rename("t1", "Migration")

	reopened := newThreadIndex(fixedStore(st))
	rec, _ := reopened.get("t1")
	if rec.Title != "Migration" || rec.Preview != "plan the migration" {
		t.Errorf("record = %+v, want both the name and the preview", rec)
	}
}

func TestPreviewOfShortensAtAWordBoundary(t *testing.T) {
	long := "please go through the entire configuration directory and tell me " +
		"everything that looks wrong or out of date"
	got := previewOf(long)

	if len([]rune(got)) > 62 {
		t.Errorf("preview is %d chars, want it short: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated preview should end in an ellipsis: %q", got)
	}
	if strings.Contains(got, " …") {
		t.Errorf("the ellipsis should follow a word, not a space: %q", got)
	}

	// Short prompts pass through whole, with whitespace tidied.
	if got := previewOf("  hi   there \n"); got != "hi there" {
		t.Errorf("previewOf() = %q, want %q", got, "hi there")
	}
}
