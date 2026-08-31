//go:build unix

package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/render"
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
