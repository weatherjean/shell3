//go:build unix

package render_test

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestDashIndexHTMLNilSafe(t *testing.T) {
	out := render.DashIndexHTML(nil, nil, "v1.2.3", nil, nil, nil, "")
	for _, want := range []string{"v1.2.3", "No live session.", "No background jobs.", "No cron jobs."} {
		if !strings.Contains(out, want) {
			t.Errorf("index fragment missing %q", want)
		}
	}
	if strings.Contains(out, "<html") {
		t.Error("fragment must not carry a page shell")
	}
}

func TestDashIndexHTMLEscapesJobs(t *testing.T) {
	jobs := []shell3.JobInfo{{ID: "bg1", Cmd: "<script>alert(1)</script>", Done: true}}
	out := render.DashIndexHTML(nil, nil, "", jobs, nil, nil, "")
	if strings.Contains(out, "<script>alert") {
		t.Fatal("job command not escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatal("escaped job command missing")
	}
}

func TestDashIndexHTMLCron(t *testing.T) {
	st := []cron.JobStatus{{Name: "sync", Schedule: "*/30 * * * *", Tool: "pull"}}
	out := render.DashIndexHTML(nil, nil, "", nil, st, nil, "")
	for _, want := range []string{"sync", "*/30 * * * *", "tool:pull", "never run", "0 tok/7d run"} {
		if !strings.Contains(out, want) {
			t.Errorf("cron section missing %q", want)
		}
	}
}

func TestRunsPageHTML(t *testing.T) {
	root, id := fixtureRun(t)
	frag, total, err := render.RunsPageHTML(root, 1, 8, "tok123")
	if err != nil {
		t.Fatalf("RunsPageHTML: %v", err)
	}
	if total != 1 {
		t.Fatalf("totalPages = %d, want 1", total)
	}
	if !strings.Contains(frag, "/runs/"+id+"?t=tok123") {
		t.Fatalf("row link with token missing:\n%s", frag)
	}
	if !strings.Contains(frag, "count the go files") {
		t.Error("first prompt missing")
	}
	if _, _, err := render.RunsPageHTML(root, 0, 8, "t"); err == nil {
		t.Error("page 0 accepted")
	}
	frag, total, err = render.RunsPageHTML(root, 9, 8, "t")
	if err != nil || frag != "" || total != 1 {
		t.Errorf("past-end page: frag=%q total=%d err=%v", frag, total, err)
	}
}

func TestRunsPageHTMLEmptyStore(t *testing.T) {
	frag, total, err := render.RunsPageHTML(t.TempDir(), 1, 8, "t")
	if err != nil || total != 1 || !strings.Contains(frag, "No runs stored.") {
		t.Fatalf("empty store: frag=%q total=%d err=%v", frag, total, err)
	}
}
