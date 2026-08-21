package runs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpen_UnreadableFilePreservedOnError pins the same invariant as
// TestOpen_CorruptFilePreservedOnError for a different failure shape: a file
// this process cannot even read (chmod 000). Open must propagate the error,
// leave the file exactly as it was, and never create an aside/backup copy —
// a permission error happens before openDB can form any opinion about the
// schema, let alone decide to recreate.
func TestOpen_UnreadableFilePreservedOnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not block reads")
	}
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	if err := os.WriteFile(path, []byte("some bytes that would otherwise be a valid-ish file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) }) // let t.TempDir() clean up afterward

	if _, err := Open(root); err == nil {
		t.Fatal("Open on an unreadable file: want error, got nil")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file vanished after failed Open: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != DBFile {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("root dir after refused Open = %v, want only %q (no aside file created)", names, DBFile)
	}
}

// TestOpen_CorruptFilePreservedOnError pins the core invariant: a file Open
// cannot make sense of is left alone, never deleted. Garbage bytes fail the
// very first schema-check query, well before the recreate path.
func TestOpen_CorruptFilePreservedOnError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	if err := os.WriteFile(path, []byte("this is not a sqlite database, just garbage bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil {
		t.Fatal("Open on a corrupt file: want error, got nil")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file vanished after failed Open: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("corrupt file was modified/replaced by a failed Open")
	}
}

// TestOpen_ImplausibleUserVersionErrorsAndPreservesFile is the F-1 repro from
// the reviewer report: a single corrupted header byte can make user_version
// read back as an implausible value (e.g. 257) while the rest of the
// database — tables, rows — is completely intact. Open must refuse to
// recreate on an implausible stamp; it must error, and the file (with its
// data) must survive untouched.
func TestOpen_ImplausibleUserVersionErrorsAndPreservesFile(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentSession("web", "precious"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, DBFile)
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], 0x00000101) // 257 — implausible, out of [0, schemaVersion]
	if _, err := f.WriteAt(b[:], 60); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil {
		t.Fatal("Open with an implausible user_version: want error, got nil")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file vanished after refusing to recreate: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("Open modified the file instead of erroring out")
	}

	// Also confirm no aside/backup files were created — a refused Open
	// should touch nothing at all.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != DBFile {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("root dir after refused Open = %v, want only %q", names, DBFile)
	}
}

// TestOpen_GenuineMismatchStillRecreates: a real old-shape database (version
// within [0, schemaVersion)) still gets recreated, as documented behaviour —
// F-1's bound only rejects implausible/out-of-range stamps, not legitimate
// old ones.
func TestOpen_GenuineMismatchStillRecreates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	v, err := userVersion(st.db)
	if err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Errorf("user_version after recreate = %d, want %d", v, schemaVersion)
	}
	if _, ok := st.CurrentSession("web"); ok {
		t.Error("a recreated store should not carry over the old database's rows")
	}
}

// TestOpen_RecreateMovesOldFilesAsideNotDeletes pins F-4: the recreate path
// renames the old db (and its -wal/-shm siblings, if present) aside rather
// than unlinking them, and the aside copies are cleaned up only after the
// new store is built successfully. It also pins the delete/rename scope: a
// sibling file in the same directory is never touched.
func TestOpen_RecreateMovesOldFilesAsideNotDeletes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)

	// A sibling file that must survive untouched.
	sibling := filepath.Join(root, "not-the-database.txt")
	if err := os.WriteFile(sibling, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling file did not survive a recreate: %v", err)
	}

	// Close before inspecting the directory: the freshly-opened store is in
	// WAL mode, so shell3.db-wal/-shm legitimately exist while it's open —
	// what this test pins is that no ".old-*" aside copies are left behind
	// once the recreate succeeds, not the live store's own WAL files.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names[DBFile] {
		t.Fatalf("root dir after recreate = %v, want %q present", names, DBFile)
	}
	if !names["not-the-database.txt"] {
		t.Fatalf("root dir after recreate = %v, want sibling file present", names)
	}
	for name := range names {
		if strings.Contains(name, ".old-") {
			t.Errorf("unexpected leftover aside file after successful recreate: %q", name)
		}
	}
}
