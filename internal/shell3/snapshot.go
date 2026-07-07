package shell3

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// ParamValue is one tunable provider parameter and its current/default state,
// for introspection (the read side of SetParam). Enum is empty for freeform
// params. Value is "" when the param is at its provider default (unset).
type ParamValue struct {
	Name        string
	Value       string
	Default     string
	Description string
	Enum        []string
}

// Snapshot is a read-only view of the session's current agent state: everything
// the TUI's welcome banner, status bar, /prompt, /info, and /parameters list
// need. It is a point-in-time copy; mutate the Session (e.g.
// SwitchAgent, SetParam) and call Snapshot again to observe changes. Safe to
// call concurrently with a running turn: cfg reads are taken under s.mu against
// the between-turns writers (a front-end may poll it mid-turn).
type Snapshot struct {
	Agent         string
	Model         string
	StatusLine    string
	ContextWindow int
	SystemPrompt  string
	Skills        []string
	Subagents     []string
	Params        []ParamValue
	// ToolHooksOn reports whether shell3.on_tool_call hooks are declared in the
	// loaded config. When false the shell is unsafe by default, which the TUI
	// surfaces with a standing "!" indicator.
	ToolHooksOn bool
	// Warnings are non-fatal config load issues (e.g. a removed config key
	// that is now ignored). A front-end surfaces them in-band at startup — the
	// alt-screen TUI otherwise clears the stderr line they were printed on.
	Warnings []string
	// Theme holds config-global TUI color overrides (token → "#RRGGBB") from
	// shell3.theme{}. A front-end applies them atop its palette; empty means the
	// built-in palette is used unchanged.
	Theme map[string]string
	// Welcome is a custom welcome-card string (shell3.welcome), rendered verbatim
	// in place of the built-in card. Empty means the built-in card is shown.
	Welcome string
}

// Snapshot returns the current agent state (see Snapshot). Params is populated
// only when the active provider implements llm.ParamDescriber.
func (s *Session) Snapshot() Snapshot {
	// Copy the cfg fields out under s.mu so a concurrent cfg writer (SwitchAgent,
	// SetParam, Clear, RegisterHostTool — all between turns) can't race the read.
	// Release before SplitStatus/ParamSpecs so we never hold s.mu across the
	// provider's ParamSpecs() call.
	s.mu.Lock()
	// The displayed prompt is the authored prompt PLUS the host standing
	// reminders (Environment, Delegation) — they're injected into every turn but
	// kept out of cfg.Personality.SystemPrompt, so the /prompt view and the
	// dashboard Status → Prompt surface the full effective context here.
	systemPrompt := s.cfg.Personality.SystemPrompt
	if rems := s.sess.StandingReminders(); len(rems) > 0 {
		systemPrompt += "\n\n## Host reminders (injected each turn — not part of the authored prompt above)\n\n" + strings.Join(rems, "\n\n")
	}
	snap := Snapshot{
		Agent:         s.cfg.ModeLabel,
		StatusLine:    s.cfg.StatusLine,
		ContextWindow: s.cfg.ContextWindow,
		SystemPrompt:  systemPrompt,
		Skills:        slices.Clone(s.cfg.ActiveSkills),
		Subagents:     slices.Clone(s.cfg.Subagents),
		ToolHooksOn:   s.cfg.RunToolCall != nil,
		Warnings:      slices.Clone(s.cfg.ConfigWarnings),
		Theme:         maps.Clone(s.cfg.Theme),
		Welcome:       s.cfg.Welcome,
	}
	params := s.cfg.Params
	describer, ok := s.cfg.LLM.(llm.ParamDescriber)
	s.mu.Unlock()

	_, snap.Model = chat.SplitStatus(snap.StatusLine)
	if ok {
		for _, spec := range describer.ParamSpecs() {
			snap.Params = append(snap.Params, ParamValue{
				Name:    spec.Name,
				Value:   currentParamValue(params, spec.Name),
				Default: spec.Default,
				Enum:    spec.Enum,
			})
		}
	}
	return snap
}

// MessageCount returns the number of messages currently in the conversation
// (e.g. for a resumed-session "N messages in context" marker). Safe to call
// concurrently with a running turn.
func (s *Session) MessageCount() int {
	return len(s.sess.Messages())
}

// Prune replaces the tool result with the given tool-call id by a short stub,
// freeing its context-window space (= the TUI's /prune <id>). summary is the
// human-readable status string. Returns ErrBusy while a turn is in flight
// (mutates history; see ErrBusy), or an error naming the id when no tool
// result with that id exists.
func (s *Session) Prune(id string) (summary string, err error) {
	err = s.withIdle(func() error {
		msgs := s.sess.Messages()
		out, ok := chat.PruneByID(id, "pruned by user", msgs)
		if !ok {
			return fmt.Errorf("shell3: no tool result with id %q", id)
		}
		s.sess.SetMessages(msgs)
		summary = out
		return nil
	})
	return summary, err
}

// QueueCompact requests a compaction before the next turn acts (= the TUI's
// :compact). It does not compact immediately — the next turn summarizes the
// conversation before the model does anything.
func (s *Session) QueueCompact() { s.sess.QueueCompact() }

// SetParam sets a tunable provider parameter for subsequent turns. When the
// active provider implements
// llm.ParamDescriber the value is validated against that param's spec first;
// the new params are then pushed to the provider if it implements
// llm.ParamSetter. Setting reasoning_effort also re-derives the status line so
// the next Snapshot reflects it. Call only between turns (mutates cfg);
// returns ErrBusy while a turn is in flight.
func (s *Session) SetParam(name, value string) error {
	// The whole body runs under s.mu (withIdle): the busy gate, the cfg.LLM
	// read, and the cfg mutations (Params, StatusLine) are one critical
	// section, so neither a starting turn nor the dashboard's Snapshot read
	// can interleave.
	return s.withIdle(func() error {
		describer, _ := s.cfg.LLM.(llm.ParamDescriber)
		setter, _ := s.cfg.LLM.(llm.ParamSetter)

		if describer != nil {
			found := false
			for _, sp := range describer.ParamSpecs() {
				if sp.Name == name {
					if err := sp.Validate(value); err != nil {
						return err
					}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown parameter %q for this provider", name)
			}
		}
		if err := s.cfg.Params.SetByName(name, value); err != nil {
			return err
		}
		if setter != nil {
			setter.SetParams(s.cfg.Params)
		}
		if name == "reasoning_effort" {
			prov, model := chat.SplitStatus(s.cfg.StatusLine)
			if prov != "" && model != "" {
				s.cfg.StatusLine = chat.FormatStatus(prov, model, s.cfg.Params.ReasoningEffort)
			}
		}
		return nil
	})
}

// currentParamValue maps a RequestParams field to its display string for the
// given parameter name — the single source of Snapshot's ParamValue.Value
// mapping. "" means "unset (provider default)".
func currentParamValue(p llm.RequestParams, name string) string {
	switch name {
	case "reasoning_effort":
		return p.ReasoningEffort
	case "parallel_tool_calls":
		if p.ParallelToolCalls == nil {
			return ""
		}
		return fmt.Sprintf("%t", *p.ParallelToolCalls)
	case "temperature":
		if p.Temperature == nil {
			return ""
		}
		return fmt.Sprintf("%g", *p.Temperature)
	case "max_tokens":
		if p.MaxTokens == 0 {
			return ""
		}
		return fmt.Sprintf("%d", p.MaxTokens)
	}
	return ""
}
