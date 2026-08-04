package runs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpen_ResetNoticeDoesNotPromiseAsidePath pins the item-1 fix: the stderr
// notice printed on a genuine schema mismatch is unconditional (it always
// fires), but the success path (see TestOpen_RecreateMovesOldFilesAsideNotDeletes)
// removes the aside copies once the new store is built — so the notice must
// not name a recovery path that the common (successful) case then deletes.
// Before the fix it read "...old data moved aside to <path>.old-...", which
// is false in exactly the case an operator is most likely to read it.
func TestOpen_ResetNoticeDoesNotPromiseAsidePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	st, openErr := Open(root)
	os.Stderr = origStderr
	_ = w.Close()

	captured, _ := io.ReadAll(r)
	line := string(captured)

	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	defer st.Close()

	if !strings.Contains(line, "resetting") {
		t.Fatalf("reset notice missing the word %q: %q", "resetting", line)
	}
	if !strings.Contains(line, "disposable by design") {
		t.Fatalf("reset notice dropped the house-rule wording: %q", line)
	}
	if strings.Contains(line, ".old-") {
		t.Fatalf("reset notice names an aside path the success path then deletes: %q", line)
	}
}

// TestRecreateErr_NamesEveryAsideFileAndWarnsAboutPairing pins item 2: when a
// rebuild fails after files were moved aside, the returned error names every
// aside file and warns that they must be renamed back together — the main db
// and its -wal/-shm siblings share only a trailing ".old-..." suffix, not a
// name SQLite pairs on its own, so restoring only one leaves a stale/mismatched
// sibling next to it.
func TestRecreateErr_NamesEveryAsideFileAndWarnsAboutPairing(t *testing.T) {
	asided := []string{
		"/tmp/x/shell3.db.old-v1-123",
		"/tmp/x/shell3.db-wal.old-v1-123",
		"/tmp/x/shell3.db-shm.old-v1-123",
	}
	cause := os.ErrPermission
	err := recreateErr(asided, cause)
	if err == nil {
		t.Fatal("recreateErr returned nil")
	}
	msg := err.Error()
	for _, f := range asided {
		if !strings.Contains(msg, f) {
			t.Errorf("recreateErr message missing aside path %q: %q", f, msg)
		}
	}
	if !strings.Contains(msg, "together") {
		t.Errorf("recreateErr message doesn't tell the operator to rename the files back together: %q", msg)
	}
	if !strings.Contains(msg, "SQLite") {
		t.Errorf("recreateErr message doesn't explain why partial recovery is unsafe: %q", msg)
	}
}

// TestRecreateErr_NoAsideDoesNotInventAPath: when nothing was moved aside
// (e.g. the very first stat failed), the error must not claim a recovery
// path exists.
func TestRecreateErr_NoAsideDoesNotInventAPath(t *testing.T) {
	err := recreateErr(nil, os.ErrPermission)
	if err == nil {
		t.Fatal("recreateErr returned nil")
	}
	if strings.Contains(err.Error(), "moved aside") {
		t.Errorf("recreateErr with no aside files still claims a recovery path: %q", err.Error())
	}
}

// TestMoveOldFilesAside_ScopeIsExactlyDBWalShm pins item 4: given a database
// plus real -wal/-shm siblings plus an unrelated sibling file, moveOldFilesAside
// touches exactly the three db-family files and leaves everything else alone.
func TestMoveOldFilesAside_ScopeIsExactlyDBWalShm(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	walPath := path + "-wal"
	shmPath := path + "-shm"
	unrelated := filepath.Join(root, "not-the-database.txt")

	writeFile(t, path, "main db bytes")
	writeFile(t, walPath, "wal bytes")
	writeFile(t, shmPath, "shm bytes")
	writeFile(t, unrelated, "leave me alone")

	asided, err := moveOldFilesAside(path, ".old-v1-999")
	if err != nil {
		t.Fatalf("moveOldFilesAside: %v", err)
	}

	wantAsided := map[string]string{
		path + ".old-v1-999":    "main db bytes",
		walPath + ".old-v1-999": "wal bytes",
		shmPath + ".old-v1-999": "shm bytes",
	}
	if len(asided) != len(wantAsided) {
		t.Fatalf("asided = %v, want exactly %v", asided, wantAsided)
	}
	for _, f := range asided {
		want, ok := wantAsided[f]
		if !ok {
			t.Errorf("unexpected file moved aside: %q", f)
			continue
		}
		got, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("aside file %q not readable: %v", f, err)
			continue
		}
		if string(got) != want {
			t.Errorf("aside file %q content = %q, want %q", f, got, want)
		}
	}

	// The originals are gone from their original names.
	for _, orig := range []string{path, walPath, shmPath} {
		if _, err := os.Stat(orig); !os.IsNotExist(err) {
			t.Errorf("original file %q still present after aside (stat err = %v)", orig, err)
		}
	}

	// The unrelated sibling was never touched.
	got, err := os.ReadFile(unrelated)
	if err != nil {
		t.Fatalf("unrelated sibling file vanished: %v", err)
	}
	if string(got) != "leave me alone" {
		t.Fatalf("unrelated sibling file was modified: %q", got)
	}
}

// TestMoveOldFilesAside_NoWalShmIsFine: a db with no -wal/-shm siblings moves
// aside cleanly, asided containing only the main file.
func TestMoveOldFilesAside_NoWalShmIsFine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	writeFile(t, path, "main db bytes")

	asided, err := moveOldFilesAside(path, ".old-v0-1")
	if err != nil {
		t.Fatalf("moveOldFilesAside: %v", err)
	}
	if len(asided) != 1 || asided[0] != path+".old-v0-1" {
		t.Fatalf("asided = %v, want exactly [%q]", asided, path+".old-v0-1")
	}
}

// TestOpen_RecreateWithWalShmPresentSurvivesAndCleansUp is the item-4
// integration-level counterpart: a genuine mismatched db with real -wal/-shm
// siblings and an unrelated file present at recreate time. Open succeeds, the
// unrelated file survives untouched, and once the store is closed no ".old-"
// leftovers remain — the wal/shm aside-and-cleanup cycle exercised end to end
// (the exact rename scope itself is pinned precisely by
// TestMoveOldFilesAside_ScopeIsExactlyDBWalShm above, since the success path
// here deletes the aside copies before we can inspect them directly).
func TestOpen_RecreateWithWalShmPresentSurvivesAndCleansUp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)
	writeFile(t, path+"-wal", "stale wal bytes")
	writeFile(t, path+"-shm", "stale shm bytes")
	unrelated := filepath.Join(root, "not-the-database.txt")
	writeFile(t, unrelated, "leave me alone")

	st, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(unrelated)
	if err != nil || string(got) != "leave me alone" {
		t.Fatalf("unrelated sibling did not survive: content=%q err=%v", got, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".old-") {
			t.Errorf("leftover aside file after successful recreate: %q", e.Name())
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
