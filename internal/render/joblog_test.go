//go:build unix

package render_test

import (
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestJobLogPageHTML(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.JobLogPath(id, "bg1"), []byte("hello <script>bad</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := render.JobLogPageHTML(root, id, "bg1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, "hello &lt;script&gt;bad") {
		t.Fatalf("bad job page:\n%s", page)
	}
	if strings.Contains(page, "<script>bad</script>") || strings.Contains(page, "href=") {
		t.Fatalf("job page contains active markup or links:\n%s", page)
	}
}
