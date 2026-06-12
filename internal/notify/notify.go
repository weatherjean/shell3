// Package notify defines the cross-process completion notification carried over
// the socket transport and parked in the SQLite inbox.
package notify

const (
	KindBgDone    = "bg_done"
	KindAgentDone = "agent_done"
)

type Notification struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	Status     string `json:"status,omitempty"`
	Exit       *int   `json:"exit,omitempty"`
	Log        string `json:"log,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Preview    string `json:"preview,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	TS         string `json:"ts"`
	// Origin is the session id this notification is about (the completing
	// child), so a cascaded delivery can name the true source even when it
	// surfaces several hops up.
	Origin int64 `json:"origin,omitempty"`
}
