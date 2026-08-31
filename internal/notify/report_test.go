package notify

import "testing"

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

func TestParseReportMode(t *testing.T) {
	if m, err := ParseReportMode(""); err != nil || m != ReportAuto {
		t.Fatalf(`"" = %v, %v; want auto`, m, err)
	}
	if _, err := ParseReportMode("alwyas"); err == nil {
		t.Fatal("a typo'd mode must be rejected, not defaulted")
	}
}
