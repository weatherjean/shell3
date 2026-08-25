package notify

import "testing"

// Every mode round-trips through its wire name, which is the same spelling in
// the kit YAML and in a tool call — one name per mode, or an operator reading
// the dash learns a word the tool schema does not accept.
func TestReportModeRoundTrip(t *testing.T) {
	for _, m := range []ReportMode{ReportAuto, ReportAlways, ReportRaw} {
		got, err := ParseReportMode(m.String())
		if err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		if got != m {
			t.Fatalf("%v round-tripped to %v", m, got)
		}
	}
}

// An omitted field is auto, and a typo is an ERROR rather than a silent
// fallback to auto: `report: alwyas` must not quietly hand back the one mode
// whose whole purpose is that it can be overridden.
func TestParseReportMode(t *testing.T) {
	if m, err := ParseReportMode(""); err != nil || m != ReportAuto {
		t.Fatalf(`"" = %v, %v; want auto`, m, err)
	}
	if _, err := ParseReportMode("alwyas"); err == nil {
		t.Fatal("a typo'd mode must be rejected, not defaulted")
	}
}
