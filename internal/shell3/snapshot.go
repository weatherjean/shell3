package shell3

import (
	"slices"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
)

// ToolInfo names a tool exposed by the active agent and its one-line
// description, for the Status view.
type ToolInfo struct {
	Name        string
	Description string
}

// Snapshot is a read-only view of the session's current agent state: everything
// the Status view needs. It is a point-in-time copy; mutate the
// Session (e.g. RegisterHostTool) and call Snapshot again to observe
// changes. Safe to call concurrently with a running turn: cfg reads are taken
// under s.mu against the between-turns writers (a front-end may poll it mid-turn).
type Snapshot struct {
	Agent         string
	Model         string
	ContextWindow int
	SystemPrompt  string
	Skills        []string
	Subagents     []string
	// Tools lists the active agent's advertised tools (built-ins, custom, and
	// host-registered), for introspection and front-end feature gating.
	Tools []ToolInfo
	// ToolHooksOn reports whether tool-call hook scripts are declared in the
	// loaded config. When false the shell is unsafe by default, which a front-end
	// surfaces with a standing "!" indicator.
	ToolHooksOn bool
	// Warnings are non-fatal config load issues (e.g. a skipped invalid skill
	// file). A front-end surfaces them in-band at startup — a Telegram or
	// alternate-screen user otherwise never sees the stderr line they were
	// printed on.
	Warnings []string
	// MCP lists every declared MCP server's live health (nil when no
	// mcp: block is declared).
	MCP []chat.MCPServerStatus
}

// Snapshot returns the current agent state (see Snapshot).
func (s *Session) Snapshot() Snapshot {
	// Copy the cfg fields out under s.mu so a concurrent cfg writer (reload or
	// RegisterHostTool, both between turns) can't race the read. Release
	// before calling the live MCP status closure.
	s.mu.Lock()
	// The displayed prompt is the authored prompt PLUS the host standing
	// reminders (Environment) — they're injected into every turn but
	// kept out of cfg.Profile.SystemPrompt, so the status document
	// surfaces the full effective context here.
	systemPrompt := s.cfg.Profile.SystemPrompt
	if rems := s.sess.StandingReminders(); len(rems) > 0 {
		systemPrompt += "\n\n## Host reminders (injected each turn — not part of the authored prompt above)\n\n" + strings.Join(rems, "\n\n")
	}
	snap := Snapshot{
		Agent:         s.cfg.Agent,
		Model:         s.cfg.ModelID,
		ContextWindow: s.cfg.ContextWindow,
		SystemPrompt:  systemPrompt,
		Skills:        slices.Clone(s.cfg.ActiveSkills),
		Subagents:     slices.Clone(s.cfg.Subagents),
		ToolHooksOn:   s.cfg.RunToolCall != nil,
		Warnings:      slices.Clone(s.cfg.ConfigWarnings),
	}
	for _, t := range s.cfg.Profile.Tools {
		snap.Tools = append(snap.Tools, ToolInfo{Name: t.Name, Description: t.Description})
	}
	mcpStatus := s.cfg.MCPStatus
	s.mu.Unlock()

	// Outside s.mu: the status closure locks the MCP manager's own mutexes.
	if mcpStatus != nil {
		snap.MCP = mcpStatus()
	}

	return snap
}

// MessageCount returns the number of messages currently in the conversation
// (e.g. for a resumed-session "N messages in context" marker). Safe to call
// concurrently with a running turn.
func (s *Session) MessageCount() int {
	return len(s.sess.Messages())
}
