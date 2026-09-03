// Package notify defines background-command completion notices.
package notify

// Kind discriminates completion notifications. Typed so a mistyped kind is a
// compile error, not a silently unmatched switch arm.
type Kind string

const (
	KindBgDone Kind = "bg_done"
)

// Notification is one completion event surfaced into a live agent's context.
type Notification struct {
	Kind    Kind
	ID      string // background job id
	Status  string // free-form completion status
	Exit    *int   // process exit code, if known
	Preview string // bounded output tail
	Cmd     string // command that ran
	Detail  string // path to the complete output, if persisted
}
