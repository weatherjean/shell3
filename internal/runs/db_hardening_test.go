package runs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

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
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

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

func TestOpen_ImplausibleUserVersionResetsStore(t *testing.T) {
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
	binary.BigEndian.PutUint32(b[:], 0x00000101)
	if _, err := f.WriteAt(b[:], 60); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, ok := reopened.CurrentSession("web"); ok {
		t.Fatal("mismatched store row survived reset")
	}
	v, err := userVersion(reopened.db)
	if err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, err=%v; want %d", v, err, schemaVersion)
	}
}

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

func TestOpen_RecreateDeletesOldDatabaseButPreservesSibling(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)

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
		if name != DBFile && name != "not-the-database.txt" {
			t.Errorf("unexpected backup or sidecar after reset: %q", name)
		}
	}
}
