package render

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

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
