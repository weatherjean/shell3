//go:build unix

package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestStatusPageHTMLIsStandaloneAndEscaped(t *testing.T) {
	page := render.StatusPageHTML(nil, nil, "v1<bad>", nil, nil, nil,
		[]render.RoomInfo{{ChatID: 42, Title: "<script>x</script>", SessionID: "run1"}},
		"queued <thing>", time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	for _, want := range []string{"<!doctype html>", "2026-08-31 12:00:00 UTC", "run1", "queued &lt;thing&gt;"} {
		if !strings.Contains(page, want) {
			t.Errorf("status missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<script>x</script>") || strings.Contains(page, "href=") {
		t.Fatalf("status contains active markup or dashboard links:\n%s", page)
	}
}

func TestStatusPageHTMLRendersJobStatesAndEscapesContent(t *testing.T) {
	exit := 7
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	page := render.StatusPageHTML(nil, nil, "", []shell3.JobInfo{
		{ID: "bg<1>", Kind: shell3.JobCommand, Cmd: "echo <unsafe>", StartedAt: now.Add(-3 * time.Second)},
		{ID: "bg2", Kind: shell3.JobCommand, Cmd: "false", Done: true, Exit: &exit, ParentSession: "run&1"},
		{ID: "sub1", Kind: shell3.JobSubagent, Agent: "reviewer", Cmd: "inspect", Done: true, Error: "bad <result>"},
	}, nil, nil, nil, "", now)

	for _, want := range []string{"bg&lt;1&gt;", "echo &lt;unsafe&gt;", "exit 7", "reviewer: inspect", "bad &lt;result&gt;", "run&amp;1"} {
		if !strings.Contains(page, want) {
			t.Errorf("status missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<unsafe>") || strings.Contains(page, "bad <result>") {
		t.Fatal("job content escaped into active markup")
	}
}
