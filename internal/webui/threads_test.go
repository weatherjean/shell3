//go:build unix

package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThreadIndexPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")

	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	ti.record("thread-a", "sess-1", "")
	ti.record("thread-b", "sess-2", "")
	if err := ti.close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()

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

// Re-recording the same pair every turn must not grow the file.
func TestThreadIndexSkipsRedundantWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ti.close()

	for range 10 {
		ti.record("thread-a", "sess-1", "")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 1 {
		t.Errorf("file has %d lines, want 1", lines)
	}
}

// A crash mid-append leaves a partial final line; reopening must drop it and
// land the next write on a clean record boundary.
func TestThreadIndexHealsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	torn := `{"t":"thread-a","s":"sess-1"}` + "\n" + `{"t":"thread-b","s":"se`
	if err := os.WriteFile(path, []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}

	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ti.lookup("thread-b"); ok {
		t.Error("the torn record should not load")
	}
	ti.record("thread-c", "sess-3", "")
	ti.close()

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()

	if got, ok := reopened.lookup("thread-c"); !ok || got != "sess-3" {
		t.Errorf("the write after a torn tail was lost: %q, %v", got, ok)
	}
	if got, ok := reopened.lookup("thread-a"); !ok || got != "sess-1" {
		t.Errorf("the intact record was lost: %q, %v", got, ok)
	}
}

func TestPruneThreadIndexDropsDeadSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	ti.record("keep", "sess-live", "")
	ti.record("drop", "sess-swept", "")
	ti.close()

	removed, err := PruneThreadIndex(path, func(id string) bool { return id == "sess-live" })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()

	if _, ok := reopened.lookup("drop"); ok {
		t.Error("the swept session's thread should be gone")
	}
	if _, ok := reopened.lookup("keep"); !ok {
		t.Error("the live session's thread should survive")
	}
}

// Nothing to drop means no rewrite at all — no torn-tail risk from a pointless
// write.
func TestPruneThreadIndexNoopKeepsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, _ := newThreadIndex(path)
	ti.record("keep", "sess-live", "")
	ti.close()

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := PruneThreadIndex(path, func(string) bool { return true })
	if err != nil || removed != 0 {
		t.Fatalf("removed = %d, err = %v; want 0, nil", removed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a no-op prune rewrote the file")
	}
}

func TestPruneThreadIndexMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.jsonl")
	removed, err := PruneThreadIndex(path, func(string) bool { return true })
	if err != nil || removed != 0 {
		t.Errorf("removed = %d, err = %v; want 0, nil", removed, err)
	}
}

// A rename must survive a restart, and must not lose the session mapping —
// a rename record carries no session id of its own.
func TestThreadIndexRenameSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	ti.record("t1", "sess-1", "")
	if _, ok := ti.rename("t1", "Deploy notes"); !ok {
		t.Fatal("rename should find the thread")
	}
	ti.close()

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()

	rec, ok := reopened.get("t1")
	if !ok || rec.Title != "Deploy notes" {
		t.Errorf("record = %+v, want the renamed thread", rec)
	}
	if got, ok := reopened.lookup("t1"); !ok || got != "sess-1" {
		t.Errorf("session mapping lost across rename: %q, %v", got, ok)
	}
}

// A deleted thread disappears from the listing and stays gone across a
// restart, even though the log is append-only.
func TestThreadIndexDeleteIsPermanent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	ti.record("gone", "sess-1", "")
	if !ti.remove("gone") {
		t.Fatal("remove should report success")
	}
	if len(ti.list()) != 0 {
		t.Error("a deleted thread should not appear in the listing")
	}
	ti.close()

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if _, ok := reopened.get("gone"); ok {
		t.Error("a deleted thread came back after a restart")
	}
}

// Threads written before they carried timestamps still know their session id,
// whose runs-store prefix is a creation time.
func TestThreadIndexBackfillsTimeFromSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	legacy := `{"t":"old","s":"20260725T181054.874072000-a308418b"}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ti.close()

	rec, ok := ti.get("old")
	if !ok {
		t.Fatal("the legacy record should load")
	}
	if !strings.HasPrefix(rec.Created, "2026-07-25T") {
		t.Errorf("created = %q, want a time recovered from the session id", rec.Created)
	}
	if rec.Updated == "" {
		t.Error("updated should fall back to created")
	}
}

func TestTimeFromSessionIDIgnoresOtherShapes(t *testing.T) {
	for _, id := range []string{"", "not-a-session", "web-t1", "20261340T999999.0-x"} {
		if got := timeFromSessionID(id); got != "" {
			t.Errorf("timeFromSessionID(%q) = %q, want empty", id, got)
		}
	}
}

// The listing is newest-activity-first, which is the order the sidebar shows.
func TestThreadIndexListsNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ti.close()

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
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}

	ti.record("t1", "sess-1", "plan the migration")
	ti.record("t1", "sess-1", "and now something else")

	rec, _ := ti.get("t1")
	if rec.Preview != "plan the migration" {
		t.Errorf("preview = %q, want the first message", rec.Preview)
	}

	ti.close()
	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if rec, _ := reopened.get("t1"); rec.Preview != "plan the migration" {
		t.Errorf("preview = %q after restart, want it preserved", rec.Preview)
	}
}

// A rename writes no preview of its own; it must not erase the one there.
func TestRenameKeepsThePreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.jsonl")
	ti, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	ti.record("t1", "sess-1", "plan the migration")
	ti.rename("t1", "Migration")
	ti.close()

	reopened, err := newThreadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()

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
