// Package notify defines the completion-notification value rendered into a
// live agent's context when a background job or subagent finishes. Jobs run
// in-process (internal/shell3's job runtime), which injects the rendered
// notification directly into the parent session — there is no cross-process
// transport.
package notify

// Kind discriminates completion notifications. Typed so a mistyped kind is a
// compile error, not a silently unmatched switch arm.
type Kind string

const (
	KindBgDone    Kind = "bg_done"    // a background bash job finished
	KindAgentDone Kind = "agent_done" // a fire-and-forget subagent finished
	// KindAgentUpdate is a follow-up from an already-"done" subagent: one of
	// its background jobs finished after its main turn ended, the child session
	// was resumed for a follow-up turn, and this carries that turn's summary.
	KindAgentUpdate Kind = "agent_update"
)

// Notification is one completion event surfaced into a live agent's context.
type Notification struct {
	Kind    Kind   `json:"kind"`              // KindBgDone | KindAgentDone | KindAgentUpdate
	ID      string `json:"id,omitempty"`      // job or subagent id
	Status  string `json:"status,omitempty"`  // free-form completion status
	Exit    *int   `json:"exit,omitempty"`    // process exit code, if known
	Preview string `json:"preview,omitempty"` // short human-readable summary
	Cmd     string `json:"cmd,omitempty"`     // the command that ran (bg jobs)
	TS      string `json:"ts"`                // RFC3339 completion timestamp
}
