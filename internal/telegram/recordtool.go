//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/shell3"
)

// registerRecordTool lets the attached root agent send an exact stored
// transcript or command-job log after it has selected the relevant record.
// Headless sessions do not receive Telegram host tools.
func (b *Bot) registerRecordTool(s *shell3.Session) {
	_ = s.RegisterHostTool(shell3.HostTool{
		Name: "send_record_telegram",
		Description: "Send an exact stored shell3 record to the user as a self-contained HTML document. " +
			"Use kind=conversation after the history tool has identified a session. This works for main, subagent, and cron sessions. " +
			"Use kind=job_log for a bash_bg log and provide both its parent session and job id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":    map[string]any{"type": "string", "enum": []string{"conversation", "job_log"}},
				"session": map[string]any{"type": "string", "description": "Stored session id."},
				"job":     map[string]any{"type": "string", "description": "Job id; required only for kind=job_log."},
				"caption": map[string]any{"type": "string", "description": "Optional Telegram caption."},
			},
			"required": []string{"kind", "session"},
		},
		Handler: func(ctx context.Context, argsJSON string) (string, error) {
			return b.sendRecordHandler(ctx, s, argsJSON)
		},
	})
}

func (b *Bot) sendRecordHandler(ctx context.Context, s *shell3.Session, argsJSON string) (string, error) {
	var args struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Job     string `json:"job"`
		Caption string `json:"caption"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: invalid arguments: " + err.Error(), nil
	}
	args.Kind, args.Session, args.Job = strings.TrimSpace(args.Kind), strings.TrimSpace(args.Session), strings.TrimSpace(args.Job)
	var page, name string
	var err error
	switch args.Kind {
	case "conversation":
		page, err = render.RunReplayHTML(b.runsRoot, args.Session)
		name = "shell3-conversation-" + args.Session + ".html"
	case "job_log":
		if args.Job == "" {
			return "error: job is required for kind=job_log", nil
		}
		page, err = render.JobLogPageHTML(b.runsRoot, args.Session, args.Job)
		name = "shell3-job-" + args.Job + ".html"
	default:
		return "error: kind must be conversation or job_log", nil
	}
	if err != nil {
		return "error: " + err.Error(), nil
	}
	if len(page) > maxSendBytes {
		return fmt.Sprintf("error: rendered record is too large (%d MB, max 50 MB)", len(page)>>20), nil
	}
	c := b.roomOrHome(s.ID())
	if _, err := b.client.SendDocument(ctx, c.chatIDValue(), name, []byte(page), args.Caption); err != nil {
		return "error: failed to send: " + err.Error(), nil
	}
	return "sent " + name + " to the user", nil
}
