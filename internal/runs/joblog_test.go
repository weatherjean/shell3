package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobLogPath(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	p := st.JobLogPath(id, "bg1")
	if p == "" {
		t.Fatal("empty path")
	}
	if !strings.HasSuffix(p, filepath.Join(id, "jobs", "bg1.log")) {
		t.Fatalf("path = %q", p)
	}
	if err := os.WriteFile(p, []byte("out"), 0o644); err != nil {
		t.Fatalf("write to job log path: %v", err)
	}
}

func TestJobLogPathInvalidSession(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := st.JobLogPath("../../etc", "bg1")
	if p != "" && !strings.Contains(p, "invalid-session-id") {
		t.Fatalf("traversal not neutralized: %q", p)
	}
}
