//go:build unix

package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// RoomInfo is one live Telegram room in a status snapshot.
type RoomInfo struct {
	ChatID    int64
	Title     string
	Busy      bool
	Jobs      int
	Queued    int
	SessionID string
}

// StatusPageHTML renders one self-contained point-in-time status document.
// It deliberately contains no links: Telegram is the front end, and detailed
// transcripts or job logs are sent separately on request.
func StatusPageHTML(sess *shell3.Session, rt *shell3.Runtime, version string,
	jobs []shell3.JobInfo, statuses []cron.JobStatus, costs map[string]runs.JobCost,
	rooms []RoomInfo, inbox string, now time.Time) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>shell3 status</title>\n<style>%s</style>\n</head><body>\n", statusCSS)
	b.WriteString("<section><h1>shell3 status</h1><dl>\n")
	kv(&b, "generated", stamp(now))
	kv(&b, "version", version)
	if rt != nil {
		if dir, err := rt.ConfigDir(); err == nil {
			kv(&b, "config", dir)
		}
	}
	if sess == nil {
		b.WriteString("</dl><p class=\"meta\">No live session.</p></section>\n")
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
		b.WriteString("</dl>\n")
		writeNames(&b, "tools", toolNames(snap.Tools))
		writeNames(&b, "employees", snap.Subagents)
		writeNames(&b, "skills", snap.Skills)
		if len(snap.MCP) > 0 {
			b.WriteString("<h2>MCP servers</h2><ul>\n")
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
			b.WriteString("<h2>Warnings</h2><ul>\n")
			for _, w := range snap.Warnings {
				fmt.Fprintf(&b, "<li>%s</li>\n", esc(w))
			}
			b.WriteString("</ul>\n")
		}
		b.WriteString("</section>\n")
	}

	writeRooms(&b, rooms)
	writeJobs(&b, jobs)
	writeCron(&b, statuses, costs)
	if strings.TrimSpace(inbox) != "" {
		fmt.Fprintf(&b, "<section><h2>Inbox</h2><pre>%s</pre></section>\n", esc(inbox))
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func toolNames(tools []shell3.ToolInfo) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func writeNames(b *strings.Builder, label string, names []string) {
	if len(names) > 0 {
		fmt.Fprintf(b, "<p><strong>%s</strong> (%d): %s</p>\n", esc(label), len(names), esc(strings.Join(names, ", ")))
	}
}

func writeRooms(b *strings.Builder, rooms []RoomInfo) {
	b.WriteString("<section><h2>Rooms</h2>\n")
	if len(rooms) == 0 {
		b.WriteString("<p class=\"meta\">No live rooms.</p></section>\n")
		return
	}
	b.WriteString("<table><tr><th>chat</th><th>state</th><th>queued</th><th>jobs</th><th>session</th></tr>\n")
	for _, r := range rooms {
		name, state := r.Title, "idle"
		if name == "" {
			name = "(untitled)"
		}
		if r.Busy {
			state = "busy"
		}
		fmt.Fprintf(b, "<tr><td>%s <span class=\"meta\">%d</span></td><td>%s</td><td>%d</td><td>%d</td><td><code>%s</code></td></tr>\n",
			esc(name), r.ChatID, state, r.Queued, r.Jobs, esc(r.SessionID))
	}
	b.WriteString("</table></section>\n")
}

func writeJobs(b *strings.Builder, jobs []shell3.JobInfo) {
	b.WriteString("<section><h2>Background jobs</h2>\n")
	if len(jobs) == 0 {
		b.WriteString("<p class=\"meta\">No background jobs.</p></section>\n")
		return
	}
	b.WriteString("<table><tr><th>id</th><th>kind</th><th>what</th><th>state</th><th>parent session</th></tr>\n")
	for _, j := range jobs {
		state := outcome(j)
		if !j.Done {
			state = elapsed(j)
		}
		fmt.Fprintf(b, "<tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td></tr>\n",
			esc(j.ID), esc(j.Kind.String()), esc(jobLabel(j)), esc(state), esc(j.ParentSession))
	}
	b.WriteString("</table></section>\n")
}

func writeCron(b *strings.Builder, statuses []cron.JobStatus, costs map[string]runs.JobCost) {
	b.WriteString("<section><h2>Cron</h2>\n")
	if len(statuses) == 0 {
		b.WriteString("<p class=\"meta\">No cron jobs.</p></section>\n")
		return
	}
	b.WriteString("<table><tr><th>job</th><th>schedule</th><th>target</th><th>last</th><th>outcome</th><th>cost</th></tr>\n")
	for _, st := range statuses {
		fmt.Fprintf(b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			esc(st.Name), esc(st.Schedule), esc(st.Agent), esc(cronLastRun(st)), esc(cronOutcome(st)), esc(cronCost(st, costs)))
	}
	b.WriteString("</table></section>\n")
}

func kv(b *strings.Builder, name, value string) {
	if value != "" {
		fmt.Fprintf(b, "<dt>%s</dt><dd>%s</dd>\n", esc(name), esc(value))
	}
}

const statusCSS = `
:root{--bg:#fff;--fg:#1a1a1a;--dim:#666;--line:#e3e3e3;--pre:#f6f6f6}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#e6e6e6;--dim:#9a9a9a;--line:#2c2c30;--pre:#1c1c20}}
*{box-sizing:border-box}body{margin:0;padding:1rem;background:var(--bg);color:var(--fg);font:15px/1.55 ui-sans-serif,-apple-system,Segoe UI,Roboto,sans-serif;max-width:70rem}
h1{font-size:1.2rem;margin:.2rem 0 .6rem}h2{font-size:1rem;margin:1rem 0 .4rem}.meta{color:var(--dim);font-size:.85rem}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.9em}dl{display:grid;grid-template-columns:max-content 1fr;gap:.15rem .8rem;margin:.4rem 0}dt{color:var(--dim)}dd{margin:0;overflow-wrap:anywhere}
table{border-collapse:collapse;width:100%;font-size:.9rem;margin:.4rem 0}th{text-align:left;color:var(--dim);font-weight:600}th,td{border-bottom:1px solid var(--line);padding:.3rem .6rem .3rem 0;vertical-align:top;overflow-wrap:anywhere}section{margin-bottom:1.2rem}ul{margin:.3rem 0;padding-left:1.2rem}pre{background:var(--pre);border-radius:6px;padding:.6rem;white-space:pre-wrap;overflow-wrap:anywhere}
`
