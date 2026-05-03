package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/edittool"
	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestFormatEditResultShowsFiveLineCreatedPreview(t *testing.T) {
	content := strings.Join([]string{
		"line-01",
		"line-02",
		"line-03",
		"line-04",
		"line-05",
		"line-06",
		"line-07",
	}, "\n") + "\n"

	got := formatEditResult(edittool.Result{
		Path:       "/tmp/new.txt",
		NewContent: content,
		Created:    true,
		Additions:  7,
	}, true)

	for _, want := range []string{
		"Created /tmp/new.txt (+7 -0, 0→56 bytes)",
		"@@ -0,0 +1,7 @@",
		"+line-01",
		"+line-05",
		"… 2 created lines omitted",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("created preview missing %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"+line-06", "+line-07"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("created preview included line beyond five-line cap %q:\n%s", hidden, got)
		}
	}
}

func TestFormatWriteResultShowsShortCreatedPreviewWithoutOmission(t *testing.T) {
	got := formatEditResult(edittool.Result{
		Path:       "/tmp/new.txt",
		NewContent: "alpha\nbeta\n",
		Created:    true,
		Additions:  2,
	}, true)

	for _, want := range []string{
		"Created /tmp/new.txt (+2 -0, 0→11 bytes)",
		"@@ -0,0 +1,2 @@",
		"+alpha",
		"+beta",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("created write preview missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "omitted") {
		t.Fatalf("short created preview should not be omitted:\n%s", got)
	}
}

func TestColorizeEditOutputHighlightsCreatedPreviewMeta(t *testing.T) {
	input := strings.Join([]string{
		"Created /tmp/new.txt (+7 -0, 0→56 bytes)",
		"@@ -0,0 +1,7 @@",
		"+line-01",
		"… 2 created lines omitted",
	}, "\n")

	got := colorizeEditOutput(input)
	metaStyle := patchtui.BgRGB(74, 64, 24) + patchtui.Dim
	addStyle := patchtui.BgRGB(20, 60, 20) + patchtui.FgRGB(180, 230, 180)

	for _, want := range []string{
		metaStyle + "@@ -0,0 +1,7 @@" + patchtui.Reset,
		metaStyle + "… 2 created lines omitted" + patchtui.Reset,
		addStyle + "+line-01" + patchtui.Reset,
		patchtui.Dim + "Created /tmp/new.txt (+7 -0, 0→56 bytes)" + patchtui.Reset,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("colorized output missing %q:\n%q", want, got)
		}
	}
}
