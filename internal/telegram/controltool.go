//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weatherjean/shell3/internal/shell3"
)

// ControlResult is the structured, secret-free result returned by one shell3
// host-control action. Front ends own the actual operations because they know
// the active config path, generation and lifecycle.
type ControlResult map[string]any

// HostControl supplies the bounded operations exposed through the model-facing
// shell3 tool. No action accepts a path, command, PID or service-manager name:
// every operation is pinned to the running host.
type HostControl struct {
	Status         func(context.Context) (ControlResult, error)
	Validate       func(context.Context) (ControlResult, error)
	Reload         func(context.Context) (ControlResult, error)
	PrepareRestart func(context.Context) (ControlResult, error)
}

// SetHostControl installs the operations used by the shell3 tool. Install it
// before SetSessionDecorator publishes DecorateOrchestratorSession.
func (b *Bot) SetHostControl(control HostControl) {
	b.mu.Lock()
	b.control = control
	b.mu.Unlock()
}

func (b *Bot) registerControlTool(s *shell3.Session) {
	_ = s.RegisterHostTool(shell3.HostTool{
		Name: "shell3",
		Description: "Inspect and safely operate this running shell3 host. This is a host-provided control tool, " +
			"not a declaration in shell3.lisp; config_change reports only the relationship between the active and on-disk configuration. " +
			"Use status to compare the active and on-disk configuration, validate after editing shell3.lisp, " +
			"and reload to atomically activate reloadable changes for future turns. If reload reports that " +
			"restart-only fields changed, use restart; restart drains active turns and is deferred until replies " +
			"are persisted and delivered. Never kill shell3 or invoke its service manager through bash.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"status", "validate", "reload", "restart"},
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
		Handler: b.controlToolHandler,
	})
}

func (b *Bot) controlToolHandler(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	b.mu.Lock()
	control := b.control
	b.mu.Unlock()

	var run func(context.Context) (ControlResult, error)
	switch args.Action {
	case "status":
		run = control.Status
	case "validate":
		run = control.Validate
	case "reload":
		run = control.Reload
	case "restart":
		run = control.PrepareRestart
	default:
		return "", fmt.Errorf("action must be status, validate, reload, or restart")
	}
	if run == nil {
		return "", fmt.Errorf("shell3 host control is unavailable")
	}
	result, err := run(ctx)
	if err != nil {
		return "", err
	}
	if result == nil {
		result = ControlResult{}
	}
	result["action"] = args.Action
	result["tool"] = "shell3"
	result["interface_version"] = 1
	if _, ok := result["ok"]; !ok {
		result["ok"] = true
	}

	if args.Action == "restart" {
		ok, _ := result["ok"].(bool)
		if ok {
			already := b.armRestart()
			result["restart"] = "queued_after_active_replies"
			if already {
				result["restart"] = "already_queued"
			}
		}
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", errors.New("encode shell3 control result: " + err.Error())
	}
	return string(out), nil
}

// armRestart gates new model turns. Existing turns and already-queued work
// drain; the final completing turn closes restartReady after its reply send.
// It reports whether a restart was already pending.
func (b *Bot) armRestart() bool {
	b.mu.Lock()
	if b.restartPending {
		b.mu.Unlock()
		return true
	}
	b.restartPending = true
	b.mu.Unlock()

	// A text update may already be inside the debounce window. Move every
	// buffered burst into the ordinary per-room queue so the drain includes it
	// instead of letting re-exec silently discard an update already accepted.
	for _, c := range b.allConvs() {
		c.queueBurstForRestart()
	}
	return false
}

// RestartReady closes only after a requested restart has no active model turn
// left. The persistent owner may then cleanly recycle the process.
func (b *Bot) RestartReady() <-chan struct{} { return b.restartReady }

// RestartPending reports the gate state without exposing any host detail.
func (b *Bot) RestartPending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.restartPending
}

func (b *Bot) finishRestartDrain(deliveryErr error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.restartPending || b.restartSignaled {
		return
	}
	if deliveryErr != nil {
		b.restartPending = false
		b.log.Warn("deferred restart cancelled because a turn reply was not delivered", "error", deliveryErr)
		return
	}
	if b.activeTurns != 0 {
		return
	}
	b.restartSignaled = true
	close(b.restartReady)
}
