package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/notify"
)

// TaskHandler implements the background-only `task` tool: it launches a subagent
// (child session) and returns immediately.
type TaskHandler struct{}

func (TaskHandler) Name() string { return "task" }

func (TaskHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
		Description  string `json:"description"`
		Report       string `json:"report"`
		Note         string `json:"note"`
		// Direct is the REMOVED arg — see BashBgHandler's field for why it is
		// decoded rather than ignored.
		Direct *bool `json:"direct"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("task: invalid args: %w", err)
	}
	if p.Prompt == "" {
		return "", fmt.Errorf("task: prompt is required")
	}
	if cfg.StartSubagent == nil {
		return "error: subagents are not available", nil
	}
	if p.Direct != nil {
		return directRemovedMsg, nil
	}
	mode, err := notify.ParseReportMode(p.Report)
	if err != nil {
		return "", fmt.Errorf("task: %w", err)
	}
	subID, err := cfg.StartSubagent(p.SubagentType, p.Prompt, p.Description, mode, p.Note)
	if err != nil {
		return "error: " + err.Error(), nil // surfaced to the model (cap/unknown agent)
	}
	head := fmt.Sprintf("started subagent %s (@%s)", subID, p.SubagentType)
	return startedJobNotice(head, mode), nil
}

// directRemovedMsg refuses the removed `direct` arg and names its
// replacement, the way the bash-first redirect does for read_file: the job is
// NOT started, so the model re-issues the call rather than discovering the
// difference when nobody hears about the result.
const directRemovedMsg = "error: `direct` was replaced by `report`, and the job was NOT started. " +
	"Re-issue it with report:\"raw\" for the old direct:true (the output posts itself), " +
	"or report:\"always\" if you want to report back in your own words and the user is waiting. " +
	"Omit report entirely when nobody is waiting."

// startedJobNotice is what the model reads back when it launches background
// work — a subagent or a bash_bg command, which differ only in their head line.
// The mechanism is spelled out because "do not poll" alone does not hold: a
// model that thinks ending the turn abandons the user will sit in a
// task_status/sleep loop unless told the wake is how the answer arrives.
func startedJobNotice(head string, mode notify.ReportMode) string {
	const dontPoll = "Do not poll task_status and do not sleep-and-recheck in bash; waiting " +
		"in-turn blocks the conversation without making the job faster."
	switch mode {
	case notify.ReportRaw:
		return head + " (report: raw).\n" +
			"Its output posts itself to the user when it finishes — you spend no turn on it. " +
			"Finish what else you have, tell the user it is running, and END YOUR TURN. " + dontPoll
	case notify.ReportAlways:
		return head + " (report: always).\n" +
			"Its report will reach you in a later turn, and that turn MUST answer the user — " +
			"staying silent posts the raw output in your place. END YOUR TURN now. " + dontPoll
	}
	return head + ".\n" +
		"Its report will reach you in a later turn (failures always surface). Do not poll — " +
		"finish your turn. If the user is waiting on this result, you should have set " +
		"report:\"always\" (you report back) or report:\"raw\" (the output posts itself)."
}

// TaskListHandler implements the task_list tool: lists all background tasks
// (running and done) for the active runtime.
type TaskListHandler struct{}

func (TaskListHandler) Name() string { return "task_list" }

func (TaskListHandler) Execute(_ context.Context, _ string, _ json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.ListJobs == nil {
		return "no background tasks", nil
	}
	return cfg.ListJobs(), nil
}

// taskByIDHandler is the shared shape of the task tools that take one {id}
// argument and call a ToolConfig-provided func: task_status and task_cancel.
type taskByIDHandler struct {
	name string
	// fn picks the ToolConfig callback (nil ⇒ task management unavailable).
	fn func(ToolConfig) func(string) string
}

// TaskStatusHandler implements the task_status tool: returns one task's status
// and a truncated result (subagent transcript tail or command output tail).
func TaskStatusHandler() ToolHandler {
	return taskByIDHandler{name: "task_status", fn: func(cfg ToolConfig) func(string) string { return cfg.JobStatus }}
}

// TaskCancelHandler implements the task_cancel tool: cancels a running
// background task and returns a short confirmation or error.
func TaskCancelHandler() ToolHandler {
	return taskByIDHandler{name: "task_cancel", fn: func(cfg ToolConfig) func(string) string { return cfg.CancelJob }}
}

func (h taskByIDHandler) Name() string { return h.name }

func (h taskByIDHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("%s: invalid args: %w", h.name, err)
	}
	if p.ID == "" {
		return "error: id is required", nil
	}
	if fn := h.fn(cfg); fn != nil {
		return fn(p.ID), nil
	}
	return "error: task management is not available", nil
}
