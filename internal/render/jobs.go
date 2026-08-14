package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Jobs renders the background-job list: running work first, then finished.
func Jobs(jobs []shell3.JobInfo) string {
	var b strings.Builder
	b.WriteString("# Jobs\n\n")
	if len(jobs) == 0 {
		b.WriteString("_No background jobs._\n")
		return b.String()
	}

	var running, done []shell3.JobInfo
	for _, j := range jobs {
		if j.Done {
			done = append(done, j)
		} else {
			running = append(running, j)
		}
	}

	if len(running) > 0 {
		b.WriteString("## Running\n\n")
		for _, j := range running {
			fmt.Fprintf(&b, "- `%s` %s — %s _(%s)_\n", j.ID, kindOf(j), jobLabel(j), elapsed(j))
		}
		b.WriteString("\n")
	}
	if len(done) > 0 {
		b.WriteString("## Finished\n\n")
		for _, j := range done {
			fmt.Fprintf(&b, "- `%s` %s — %s _(%s)_\n", j.ID, kindOf(j), jobLabel(j), outcome(j))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// JobDetail renders one job with its captured output — a command's stdout tail
// or a subagent's transcript, whichever the caller passes.
func JobDetail(info shell3.JobInfo, transcript string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Job %s\n\n", info.ID)
	field(&b, "kind", kindOf(info))
	field(&b, "agent", info.Agent)
	field(&b, "command", oneLine(info.Cmd, 200))
	field(&b, "parent", info.ParentID)
	field(&b, "started", stamp(info.StartedAt))
	field(&b, "ended", stamp(info.EndedAt))
	if info.Done {
		field(&b, "state", "finished — "+outcome(info))
	} else {
		field(&b, "state", "running — "+elapsed(info))
	}
	if info.Exit != nil {
		field(&b, "exit", fmt.Sprintf("%d", *info.Exit))
	}
	if info.ChildOpen {
		field(&b, "child session", "still open")
	}
	b.WriteString("\n")

	if info.Error != "" {
		b.WriteString("## Error\n\n")
		fence(&b, "", info.Error)
	}
	if info.Summary != "" {
		b.WriteString("## Summary\n\n")
		b.WriteString(truncate(strings.TrimRight(info.Summary, "\n")) + "\n\n")
	}
	if strings.TrimSpace(transcript) != "" {
		b.WriteString("## Output\n\n")
		fence(&b, "", transcript)
	}
	return b.String()
}

func kindOf(j shell3.JobInfo) string {
	if j.Kind == shell3.JobSubagent {
		return "subagent"
	}
	return "command"
}

// jobLabel names the work: a subagent's agent + description, or the command text.
func jobLabel(j shell3.JobInfo) string {
	if j.Agent != "" {
		return j.Agent + ": " + oneLine(j.Cmd, 100)
	}
	return oneLine(j.Cmd, 100)
}

func elapsed(j shell3.JobInfo) string {
	if j.StartedAt.IsZero() {
		return "running"
	}
	return time.Since(j.StartedAt).Round(time.Second).String() + " so far"
}

func outcome(j shell3.JobInfo) string {
	switch {
	case j.Error != "":
		return "failed: " + oneLine(j.Error, 100)
	case j.Exit != nil && *j.Exit != 0:
		return fmt.Sprintf("exit %d", *j.Exit)
	case j.Exit != nil:
		return "exit 0"
	default:
		return "done"
	}
}

// JobsTappable renders the jobs section of /status with tappable commands.
//
// Telegram only linkifies a /command in message TEXT, never inside a
// document — which is why this section, and the view carrying it, stay inline.
// Numbering mirrors /runs: the caller stores the same index the render used,
// so a tap resolves against what you are actually looking at rather than a
// re-derived guess that a finished job would have shifted.
func JobsTappable(jobs []shell3.JobInfo) string {
	var b strings.Builder
	b.WriteString("## Jobs\n\n")
	if len(jobs) == 0 {
		b.WriteString("_None running._\n")
		return b.String()
	}
	for i, j := range jobs {
		n := i + 1
		if j.Done {
			fmt.Fprintf(&b, "%d. %s — %s _(%s)_ /job_%d\n", n, kindOf(j), jobLabel(j), outcome(j), n)
			continue
		}
		fmt.Fprintf(&b, "%d. %s — %s _(%s)_ /job_%d /cancel_%d\n", n, kindOf(j), jobLabel(j), elapsed(j), n, n)
	}
	return b.String()
}
