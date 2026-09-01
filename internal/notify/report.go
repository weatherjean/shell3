package notify

import "fmt"

// ReportMode controls how a background result reaches the chat.
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
