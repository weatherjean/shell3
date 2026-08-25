package notify

import "fmt"

// ReportMode is the ONE axis deciding what a finished background job does to
// the chat. It replaces the old `direct` bool, which answered only half the
// question ("post the raw output?") and left the other half — "may the agent
// stay silent?" — to a judgement call inside the report turn's prompt. That
// call was made wrong in the field: an agent that had just told the user "the
// report will arrive automatically" read its own start-of-job narration as
// "they already know", replied NO_REPLY, and sat on a finished install for
// nine minutes until the user asked again.
//
// The three values are mutually exclusive by construction, so the
// contradiction a second boolean would allow (direct AND must-reply) cannot
// be expressed at all.
type ReportMode int

const (
	// ReportAuto runs the agent's report turn and lets it judge whether the
	// user needs to hear anything. The default, and the only mode where
	// NO_REPLY is a legitimate answer.
	ReportAuto ReportMode = iota
	// ReportAlways runs the report turn and BINDS it to answer: a NO_REPLY or
	// empty reply is a contract violation, and the front-end posts the job's
	// own result in its place rather than letting the report vanish.
	ReportAlways
	// ReportRaw spends no agent turn at all: the job's own output posts
	// straight to the chat, and the owning session gets the notice queued
	// without a wake.
	ReportRaw
)

// String returns the wire name, which is also the YAML and tool-arg spelling.
func (m ReportMode) String() string {
	switch m {
	case ReportAuto:
		return "auto"
	case ReportAlways:
		return "always"
	case ReportRaw:
		return "raw"
	}
	return fmt.Sprintf("ReportMode(%d)", int(m))
}

// ParseReportMode reads the wire name. "" is ReportAuto — the field is
// optional everywhere it appears. An unknown value is an error rather than a
// silent fallback: a typo'd `report: alwyas` must not quietly hand back the
// mode whose whole purpose is that it can be overridden.
func ParseReportMode(s string) (ReportMode, error) {
	switch s {
	case "", "auto":
		return ReportAuto, nil
	case "always":
		return ReportAlways, nil
	case "raw":
		return ReportRaw, nil
	}
	return ReportAuto, fmt.Errorf("unknown report mode %q (want auto, always or raw)", s)
}
