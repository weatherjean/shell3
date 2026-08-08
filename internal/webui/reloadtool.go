//go:build unix

package webui

import (
	"context"

	"github.com/weatherjean/shell3/internal/shell3"
)

// The agent edits its own config directory (agents, cron jobs, skills, hooks)
// but a reload cannot run inside the very turn that asks for one — the busy
// turn is exactly what blocks it. So the tool queues: the flag is applied the
// moment the turn ends, and the result lands in the notification bell.

// RegisterReloadTool gives sess a reload tool that queues a config reload for
// when the current turn ends. A no-op for a headless session (subagent, cron
// job): applying a reload is the operator surface's business, and a subagent
// asked to edit config hands up to the main agent anyway.
func RegisterReloadTool(sess hostToolRegistrar, queue func() string) error {
	if sess.Headless() {
		return nil
	}
	return sess.RegisterHostTool(shell3.HostTool{
		Name: "reload",
		Description: "Apply configuration changes: queue a reload of the shell3 config directory " +
			"(agents, cron jobs, skills, hooks, shell3.yaml). The reload runs when this turn ends; " +
			"its result appears in the notification bell. Call this after editing the config so the " +
			"changes take effect without asking the user to restart.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler: func(context.Context, string) (string, error) {
			return queue(), nil
		},
	})
}

// queueReload marks a reload to run when the current turn ends.
func (s *Server) queueReload() string {
	s.mu.Lock()
	s.pendingReload = true
	s.mu.Unlock()
	return "reload queued — the config is re-read the moment this turn ends, and the result " +
		"is posted to the notification bell. Finish the reply normally."
}

// runPendingReload applies a reload the agent queued mid-turn. Called after
// the turn slot is released (deferred first in the turn goroutine, so it runs
// last). The result is posted to the bell either way: a reload the agent
// asked for must never fail silently.
func (s *Server) runPendingReload() {
	s.mu.Lock()
	pending := s.pendingReload
	s.pendingReload = false
	s.mu.Unlock()
	if !pending {
		return
	}
	result, ok := s.applyReloadFn()
	kind := "note"
	if !ok {
		kind = "alert"
	}
	s.publishNotification(notification{Kind: kind, Title: "config reload", Body: result})
}
