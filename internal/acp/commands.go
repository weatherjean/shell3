package acp

import (
	"context"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// acpCommand describes a slash command the ACP agent advertises to clients.
// All three built-in commands are no-argument: the client sends them as plain
// session/prompt text (e.g. "/clear"), and Prompt intercepts before the LLM runs.
type acpCommand struct {
	name        string
	description string
	// run executes the command against the session and returns the reply text.
	run func(s *acpSession) (reply string, err error)
}

// acpCommands is the authoritative registry of slash commands.
// /help's run builds its list from this registry, so order matters for display.
// Initialized by init() to avoid a self-referential package-level init cycle
// (the /help closure reads acpCommands at call time, not at init time).
var acpCommands []acpCommand

func init() {
	acpCommands = []acpCommand{
		{
			name:        "clear",
			description: "Reset the conversation history.",
			run: func(s *acpSession) (string, error) {
				if err := s.sess.Clear(); err != nil {
					return "", err
				}
				return "Conversation cleared.", nil
			},
		},
		{
			name:        "compact",
			description: "Queue compaction — it runs on your next message.",
			run: func(s *acpSession) (string, error) {
				s.sess.QueueCompact()
				return "Compaction queued — it runs on your next message.", nil
			},
		},
		{
			name:        "disable_safety",
			description: "Toggle auto-allow for the command gate (skips approval prompts).",
			run: func(s *acpSession) (string, error) {
				off := !s.sess.SafetyOff()
				s.sess.SetSafetyOff(off)
				if off {
					return "⚠️ Command gate disabled — tool actions are now auto-allowed without prompting. Run /disable_safety again to re-enable approval prompts.", nil
				}
				return "Command gate re-enabled — tool actions will prompt for approval again.", nil
			},
		},
		{
			name:        "help",
			description: "List all available slash commands.",
			// The closure reads acpCommands at call time (after init completes),
			// so it sees the fully populated registry.
			run: func(s *acpSession) (string, error) {
				var sb strings.Builder
				sb.WriteString("Available commands:\n")
				for _, cmd := range acpCommands {
					fmt.Fprintf(&sb, "  /%s — %s\n", cmd.name, cmd.description)
				}
				return strings.TrimRight(sb.String(), "\n"), nil
			},
		},
	}
}

// acpAvailableCommands converts the registry to the SDK's AvailableCommand slice.
// Names are bare (e.g. "clear"), without the leading slash — the client prepends it.
func acpAvailableCommands() []acpsdk.AvailableCommand {
	cmds := make([]acpsdk.AvailableCommand, 0, len(acpCommands))
	for _, cmd := range acpCommands {
		cmds = append(cmds, acpsdk.AvailableCommand{
			Name:        cmd.name,
			Description: cmd.description,
		})
	}
	return cmds
}

// advertiseCommands pushes an available_commands_update notification to the ACP
// client. conn is read under a.mu then released before the blocking SessionUpdate
// call — mirrors the lock discipline used throughout agent.go.
func (a *acpAgent) advertiseCommands(sessionID string) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(sessionID),
		Update: acpsdk.SessionUpdate{
			AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
				AvailableCommands: acpAvailableCommands(),
			},
		},
	})
}

// matchCommand reports whether text (after whitespace trimming) is EXACTLY a
// registered slash command (e.g. "/clear"). It returns the command and true on
// match, nil and false otherwise.
//
// No-arg discipline: the trimmed text must equal exactly "/name" — any trailing
// non-whitespace content (like "/clear the cache" or "/etc/passwd") is not matched,
// preventing interception of slash-prefixed filesystem paths or free-form text.
func matchCommand(text string) (*acpCommand, bool) {
	t := strings.TrimSpace(text)
	for i := range acpCommands {
		if t == "/"+acpCommands[i].name {
			return &acpCommands[i], true
		}
	}
	return nil, false
}
