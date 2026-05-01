package obfile_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/obfile"
)

type testData struct {
	Keys map[string]string `yaml:"keys"`
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.store")

	want := testData{Keys: map[string]string{"foo": "bar", "baz": "qux"}}
	if err := obfile.Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("foo")) || bytes.Contains(raw, []byte("bar")) {
		t.Fatal("obfile wrote plaintext")
	}

	var got testData
	if err := obfile.Read(path, &got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Keys["foo"] != "bar" || got.Keys["baz"] != "qux" {
		t.Fatalf("got %v", got.Keys)
	}
}

func TestReadMissing(t *testing.T) {
	var v testData
	err := obfile.Read("/nonexistent/path.store", &v)
	if err != nil {
		t.Fatalf("missing file should return nil, got: %v", err)
	}
}

func TestWriteCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "store.s3")
	if err := obfile.Write(path, testData{Keys: map[string]string{"a": "b"}}); err != nil {
		t.Fatalf("Write nested: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
