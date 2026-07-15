// Package notify defines the completion-notification value rendered into a
// live agent's context when a background job or subagent finishes. Jobs run
// in-process (internal/shell3's job runtime), which injects the rendered
// notification directly into the parent session — there is no cross-process
// transport.
package notify

// Kind values for Notification.Kind.
const (
	KindBgDone    = "bg_done"    // a background bash job finished
	KindAgentDone = "agent_done" // a fire-and-forget subagent finished
	// KindAgentUpdate is a follow-up from an already-"done" subagent: one of
	// its background jobs finished after its main turn ended, the child session
	// was resumed for a follow-up turn, and this carries that turn's summary.
	KindAgentUpdate = "agent_update"
)

// Notification is one completion event surfaced into a live agent's context.
type Notification struct {
	Kind       string `json:"kind"`                 // KindBgDone | KindAgentDone
	ID         string `json:"id,omitempty"`         // job or subagent id
	Status     string `json:"status,omitempty"`     // free-form completion status
	Exit       *int   `json:"exit,omitempty"`       // process exit code, if known
	Log        string `json:"log,omitempty"`        // path to the job log (bg jobs)
	Transcript string `json:"transcript,omitempty"` // path to the run transcript (subagents)
	Preview    string `json:"preview,omitempty"`    // short human-readable summary
	Cmd        string `json:"cmd,omitempty"`        // the command that ran (bg jobs)
	TS         string `json:"ts"`                   // RFC3339 completion timestamp
}
