package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

func TestRenderBackgroundNotificationNamesPersistedOutput(t *testing.T) {
	zero := 0
	got := renderNotification(notify.Notification{
		Kind: notify.KindBgDone, ID: "bg1", Cmd: "go test ./...", Exit: &zero,
		Preview: "ok", Detail: "/tmp/project/runs/session/jobs/bg1.log",
	})
	for _, want := range []string{"bg1", "go test ./...", "Output tail: ok", "Full output: /tmp/project"} {
		if !strings.Contains(got, want) {
			t.Errorf("notification %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "task_status") {
		t.Fatalf("notification advertises a removed tool: %q", got)
	}
}
