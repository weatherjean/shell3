//go:build unix

package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
)

// maxJobLogBytes caps how much of a job log the viewer shows (job logs are
// swept at 1 MiB; this bounds the tail we render into one page).
const maxJobLogBytes = 512 * 1024

// JobLogHTML renders a background job's captured output — the tee'd
// runs/<session>/jobs/<id>.log — as a dash fragment. session and id are
// validated to be single path segments (no traversal), then joined under the
// runs root's own runs/ dir — the layout runs.Store.JobLogPath writes, which
// this must not re-derive by hand. ok is false when the ids are malformed or
// the log is absent, so the caller answers 404.
func JobLogHTML(runsRoot, session, id, tok string) (frag string, ok bool) {
	if !isSegment(session) || !isSegment(id) {
		return "", false
	}
	path := filepath.Join(runsRoot, "runs", session, "jobs", id+".log")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	// Read the tail: seek to at most maxJobLogBytes before EOF so a large log
	// shows its most recent output rather than its start.
	var start int64
	if info.Size() > maxJobLogBytes {
		start = info.Size() - maxJobLogBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJobLogBytes))
	if err != nil {
		return "", false
	}
	n := len(data)

	var b strings.Builder
	b.WriteString("<section>\n<h1>Job output</h1>\n")
	fmt.Fprintf(&b, "<p class=\"meta\"><a href=\"/?t=%s\">← dashboard</a> · <code>%s</code> in <code>%s</code></p>\n",
		esc(tok), esc(id), esc(session))
	if start > 0 {
		fmt.Fprintf(&b, "<p class=\"meta\">showing the last %s of %s</p>\n", esc(humanSize(int64(n))), esc(humanSize(info.Size())))
	}
	if n == 0 {
		b.WriteString("<p class=\"meta\">(no output captured)</p>\n")
	} else {
		fmt.Fprintf(&b, "<pre><code>%s</code></pre>\n", esc(string(data)))
	}
	b.WriteString("</section>\n")
	return b.String(), true
}

// CronDetailHTML renders one cron job's full status — everything the one-line
// index row abbreviates: schedule, target, workdir, direct flag, prompt, last
// run, outcome, and cost. name selects the job; ok is false when no such job
// exists.
func CronDetailHTML(statuses []cron.JobStatus, costs map[string]runs.JobCost, name, tok string) (frag string, ok bool) {
	var st cron.JobStatus
	found := false
	for _, s := range statuses {
		if s.Name == name {
			st, found = s, true
			break
		}
	}
	if !found {
		return "", false
	}
	target := st.Agent
	if st.Tool != "" {
		target = "tool:" + st.Tool
	}
	var b strings.Builder
	b.WriteString("<section>\n")
	fmt.Fprintf(&b, "<p class=\"meta\"><a href=\"/?t=%s\">← dashboard</a></p>\n", esc(tok))
	fmt.Fprintf(&b, "<h1>cron <code>%s</code></h1>\n<dl>\n", esc(st.Name))
	kv(&b, "schedule", st.Schedule)
	kv(&b, "target", target)
	kv(&b, "workdir", st.WorkDir)
	if st.Direct {
		kv(&b, "delivery", "direct (raw post, no agent turn)")
	}
	kv(&b, "last run", cronLastRun(st))
	kv(&b, "outcome", cronOutcome(st))
	kv(&b, "cost", cronCost(st, costs))
	b.WriteString("</dl>\n")
	if strings.TrimSpace(st.Prompt) != "" {
		b.WriteString("<h2>prompt</h2>\n")
		fmt.Fprintf(&b, "<pre><code>%s</code></pre>\n", esc(st.Prompt))
	}
	b.WriteString("</section>\n")
	return b.String(), true
}

// isSegment reports whether s is a safe single path segment — non-empty, no
// separator, no "." / ".." — usable in a filesystem join without traversal.
func isSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\") && s == filepath.Base(s)
}
