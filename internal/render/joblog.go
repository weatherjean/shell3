//go:build unix

package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func humanSize(n int64) string {
	const kib = 1024
	const mib = 1024 * kib
	switch {
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// maxJobLogBytes caps how much of a job log the exported page shows (job logs are
// swept at 1 MiB; this bounds the tail we render into one page).
const maxJobLogBytes = 512 * 1024

// jobLogHTML renders a background job's captured output — the tee'd
// runs/<session>/jobs/<id>.log — as an HTML fragment. session and id are
// validated to be single path segments (no traversal), then joined under the
// runs root's own runs/ dir — the layout runs.Store.JobLogPath writes, which
// this must not re-derive by hand. ok is false when the ids are malformed or
// the log is absent, so the caller answers 404.
func jobLogHTML(runsRoot, session, id string) (frag string, ok bool) {
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
	fmt.Fprintf(&b, "<p class=\"meta\"><code>%s</code> in session <code>%s</code></p>\n",
		esc(id), esc(session))
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

// JobLogPageHTML renders one persisted background-command log as a complete
// document suitable for sending through Telegram.
func JobLogPageHTML(runsRoot, session, id string) (string, error) {
	frag, ok := jobLogHTML(runsRoot, session, id)
	if !ok {
		return "", fmt.Errorf("render: no such job log %q in session %q", id, session)
	}
	return "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n" +
		"<title>job " + esc(id) + "</title><style>" + statusCSS + "</style></head><body>\n" +
		frag + "</body></html>\n", nil
}

// isSegment reports whether s is a safe single path segment — non-empty, no
// separator, no "." / ".." — usable in a filesystem join without traversal.
func isSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\") && s == filepath.Base(s)
}
