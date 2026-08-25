//go:build unix

package render

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// This file renders the web dash's HTML FRAGMENTS — the index (status + jobs
// + cron) and the runs listing. The dash server wraps them in its page shell;
// the run replay is already a full page (RunReplayHTML) and stays one.
//
// Same escaping contract as html.go: everything that came from a model, a
// tool, or config goes through esc().

// DashIndexHTML renders the dash front page fragment. Every argument may be
// nil/empty — a dash reached before the runtime has a live session still gets
// a page. tok is the request's own access token, threaded into every link the
// fragment builds (conversation, job logs, cron detail) so a tap stays
// authenticated.
func DashIndexHTML(sess *shell3.Session, rt *shell3.Runtime, version string,
	jobs []shell3.JobInfo, statuses []cron.JobStatus, costs map[string]runs.JobCost, tok string) string {
	var b strings.Builder
	b.WriteString("<section>\n<h1>shell3</h1>\n<dl>\n")
	kv(&b, "version", version)
	if rt != nil {
		if dir, err := rt.ConfigDir(); err == nil {
			kv(&b, "config", dir)
		}
	}
	if sess == nil {
		b.WriteString("</dl>\n<p class=\"meta\">No live session.</p>\n</section>\n")
	} else {
		snap := sess.Snapshot()
		kv(&b, "agent", snap.Agent)
		kv(&b, "model", snap.Model)
		if snap.ContextWindow > 0 {
			kv(&b, "context window", fmt.Sprintf("%d tokens", snap.ContextWindow))
		}
		kv(&b, "messages in context", fmt.Sprintf("%d", sess.MessageCount()))
		gate := "not armed (no tool-call hook)"
		if snap.ToolHooksOn {
			gate = "armed"
		}
		kv(&b, "command gate", gate)
		kv(&b, "status line", snap.StatusLine)
		b.WriteString("</dl>\n")
		// Link the live transcript (the folding run-replay page) so the current
		// conversation is one tap, not a hunt through /runs.
		if id := sess.ID(); id != "" {
			fmt.Fprintf(&b, "<p><a href=\"/runs/%s?t=%s\"><strong>▶ view this conversation</strong></a></p>\n",
				esc(id), esc(tok))
		}
		if len(snap.Tools) > 0 {
			names := make([]string, 0, len(snap.Tools))
			for _, tl := range snap.Tools {
				names = append(names, tl.Name)
			}
			fmt.Fprintf(&b, "<p><strong>tools</strong> (%d): %s</p>\n", len(names), esc(strings.Join(names, ", ")))
		}
		if len(snap.Subagents) > 0 {
			fmt.Fprintf(&b, "<p><strong>employees:</strong> %s</p>\n", esc(strings.Join(snap.Subagents, ", ")))
		}
		if len(snap.Skills) > 0 {
			fmt.Fprintf(&b, "<p><strong>skills</strong> (%d): %s</p>\n", len(snap.Skills), esc(strings.Join(snap.Skills, ", ")))
		}
		if len(snap.MCP) > 0 {
			b.WriteString("<h2>MCP servers</h2>\n<ul>\n")
			for _, sv := range snap.MCP {
				state := fmt.Sprintf("%d tools", sv.ToolCount)
				if !sv.Up {
					state = "down"
					if sv.Err != "" {
						state += " — " + oneLine(sv.Err, 120)
					}
				}
				fmt.Fprintf(&b, "<li><code>%s</code> — %s</li>\n", esc(sv.Name), esc(state))
			}
			b.WriteString("</ul>\n")
		}
		if len(snap.Warnings) > 0 {
			b.WriteString("<h2>Warnings</h2>\n<ul>\n")
			for _, w := range snap.Warnings {
				fmt.Fprintf(&b, "<li>%s</li>\n", esc(w))
			}
			b.WriteString("</ul>\n")
		}
		b.WriteString("</section>\n")
	}

	b.WriteString("<section>\n<h2>Jobs</h2>\n")
	if len(jobs) == 0 {
		b.WriteString("<p class=\"meta\">No background jobs.</p>\n")
	} else {
		b.WriteString("<table>\n<tr><th>id</th><th>kind</th><th>what</th><th>state</th></tr>\n")
		for _, j := range jobs {
			state := outcome(j)
			if !j.Done {
				state = elapsed(j)
			}
			// A command (bash_bg) job tees its output to a log under its parent
			// session; link the id to that log. Subagent jobs have no such file
			// (their result is a task report), so their id stays plain text.
			// ParentSession, never ParentID: the log is under the parent's RUNS
			// id, while ParentID is the in-process handle ("s1") and names no
			// directory — linking with it 404s every job log.
			idCell := fmt.Sprintf("<code>%s</code>", esc(j.ID))
			if j.Kind == shell3.JobCommand && j.ParentSession != "" {
				idCell = fmt.Sprintf("<a href=\"/joblog?session=%s&amp;id=%s&amp;t=%s\"><code>%s</code></a>",
					urlq(j.ParentSession), urlq(j.ID), esc(tok), esc(j.ID))
			}
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				idCell, esc(j.Kind.String()), esc(jobLabel(j)), esc(state))
		}
		b.WriteString("</table>\n")
	}
	b.WriteString("</section>\n")

	b.WriteString("<section>\n<h2>Cron</h2>\n")
	if len(statuses) == 0 {
		b.WriteString("<p class=\"meta\">No cron jobs.</p>\n")
	} else {
		b.WriteString("<table>\n<tr><th>job</th><th>schedule</th><th>target</th><th>last</th><th>outcome</th><th>cost</th></tr>\n")
		for _, st := range statuses {
			target := st.Agent
			cost := cronCost(st, costs)
			fmt.Fprintf(&b, "<tr><td><a href=\"/cron?name=%s&amp;t=%s\"><code>%s</code></a></td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				urlq(st.Name), esc(tok), esc(st.Name), esc(st.Schedule), esc(target), esc(cronLastRun(st)), esc(cronOutcome(st)), esc(cost))
		}
		b.WriteString("</table>\n")
	}
	b.WriteString("</section>\n")
	return b.String()
}

// RunsPageHTML renders one page of stored runs as a fragment: newest first,
// each row linking to its replay with the caller's token threaded through.
// Same store walk and visibility rule as RunsPage (message-less sessions are
// invisible). page is 1-based; a page past the end returns an empty fragment
// with totalPages set.
func RunsPageHTML(root string, page, size int, tok string) (string, int, error) {
	if page < 1 || size < 1 {
		return "", 0, fmt.Errorf("render: invalid page %d (size %d)", page, size)
	}
	st, err := runs.Open(root)
	if err != nil {
		return "", 0, err
	}
	defer st.Close()
	all, err := st.ListSessions(0)
	if err != nil {
		return "", 0, err
	}
	metas := all[:0]
	for _, m := range all {
		if st.HasMessages(m.ID) {
			metas = append(metas, m)
		}
	}
	if len(metas) == 0 {
		return "<section><h1>Runs</h1><p class=\"meta\">No runs stored.</p></section>\n", 1, nil
	}
	totalPages := (len(metas) + size - 1) / size
	if page > totalPages {
		return "", totalPages, nil
	}
	lo := (page - 1) * size
	hi := min(lo+size, len(metas))
	var b strings.Builder
	b.WriteString("<section>\n<h1>Runs</h1>\n<table>\n<tr><th>started</th><th>status</th><th>first prompt</th></tr>\n")
	for _, m := range metas[lo:hi] {
		prompt := ""
		if msgs, err := st.LoadMessages(m.ID); err == nil {
			prompt = oneLine(firstPrompt(msgs), 80)
		}
		fmt.Fprintf(&b, "<tr><td><a href=\"/runs/%s?t=%s\">%s</a></td><td>%s</td><td>%s</td></tr>\n",
			esc(m.ID), esc(tok), esc(stamp(m.StartedAt)), esc(m.Status), esc(prompt))
	}
	b.WriteString("</table>\n<p class=\"meta\">")
	fmt.Fprintf(&b, "page %d/%d", page, totalPages)
	if page > 1 {
		fmt.Fprintf(&b, " · <a href=\"/runs?page=%d&amp;t=%s\">newer</a>", page-1, esc(tok))
	}
	if page < totalPages {
		fmt.Fprintf(&b, " · <a href=\"/runs?page=%d&amp;t=%s\">older</a>", page+1, esc(tok))
	}
	b.WriteString("</p>\n</section>\n")
	return b.String(), totalPages, nil
}

// TextSectionHTML wraps a plain-text blob (already composed by the caller,
// e.g. the bot's inbox line) as one escaped dash section. Empty text renders
// nothing.
func TextSectionHTML(title, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "<section>\n<h2>" + esc(title) + "</h2>\n<pre>" + esc(text) + "</pre>\n</section>\n"
}

// RoomInfo is one live Telegram room on the dash index. The front-end owns
// the data; render only lays it out, which is why this is a plain struct
// rather than an import of internal/telegram.
type RoomInfo struct {
	ChatID    int64
	Title     string
	Busy      bool
	Jobs      int
	Queued    int
	SessionID string
}

// RoomsSectionHTML renders the live rooms: one conversation per chat, which
// is busy, what is queued behind it, and a link to each room's transcript.
// Empty when the bot holds no conversation yet — an empty table would suggest
// something is broken when nothing is.
func RoomsSectionHTML(rooms []RoomInfo, tok string) string {
	if len(rooms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<section>\n<h2>Rooms</h2>\n")
	b.WriteString("<p class=\"meta\">One conversation per chat. All rooms share one working directory.</p>\n")
	b.WriteString("<table>\n<tr><th>chat</th><th>state</th><th>queued</th><th>jobs</th><th>transcript</th></tr>\n")
	for _, r := range rooms {
		name := r.Title
		if name == "" {
			name = "(untitled)"
		}
		state := "idle"
		if r.Busy {
			state = "busy"
		}
		fmt.Fprintf(&b, "<tr><td>%s <span class=\"meta\">%d</span></td><td>%s</td><td>%d</td><td>%d</td>",
			esc(name), r.ChatID, esc(state), r.Queued, r.Jobs)
		if r.SessionID != "" {
			fmt.Fprintf(&b, "<td><a href=\"/runs/%s?t=%s\">%s</a></td>", esc(r.SessionID), esc(tok), esc(r.SessionID))
		} else {
			b.WriteString("<td>—</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table>\n</section>\n")
	return b.String()
}

// kv writes one definition-list row, skipping empty values.
func kv(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "<dt>%s</dt><dd>%s</dd>\n", esc(name), esc(value))
}
