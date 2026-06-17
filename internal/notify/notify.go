// Package notify defines the completion-notification value rendered into a live
// agent's context. It is the in-process shape that a runs.Pointer (read from the
// project inbox) is reconstructed into before rendering; the inbox file, not this
// type, is the cross-process transport.
package notify

// Kind values for Notification.Kind.
const (
	KindBgDone    = "bg_done"    // a background bash job finished
	KindAgentDone = "agent_done" // a fire-and-forget subagent finished
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
