//go:build unix

package webui

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestStatusToolSkipsHeadlessSessions(t *testing.T) {
	reg := &fakeRegistrar{headless: true}
	if err := RegisterStatusTool(reg, func() string { return "" }); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 0 {
		t.Errorf("a headless session must not get the status tool, got %v", reg.tools)
	}

	reg = &fakeRegistrar{}
	if err := RegisterStatusTool(reg, func() string { return "" }); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 1 || reg.tools[0].Name != "status" {
		t.Errorf("an interactive session should get exactly the status tool, got %v", reg.tools)
	}
}

// The condition the run history shows the agent misdiagnosing for an
// afternoon: jobs declared in cron/ with no scheduler armed behind them.
// The digest must name that state in so many words.
func TestStatusDigestNamesUnarmedCron(t *testing.T) {
	srv := newTestServer(t, "ok")

	if d := srv.statusDigest(); !strings.Contains(d, "cron: no jobs declared") {
		t.Errorf("no jobs → digest should say so, got:\n%s", d)
	}

	srv.rt.SetCronForTest([]shell3.CronJob{
		{Name: "leads", Schedule: "@every 30m", Agent: "assistant", Prompt: "tick"},
	})
	d := srv.statusDigest()
	if !strings.Contains(d, "DECLARED BUT NOT ARMED") {
		t.Errorf("declared job with no scheduler must be called out, got:\n%s", d)
	}
	if !strings.Contains(d, "leads: @every 30m → assistant · never run") {
		t.Errorf("the job line should carry schedule, agent, and last run, got:\n%s", d)
	}

	sched, err := cron.New(nil, srv.rt.Cron())
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCronSource(sched)
	if d := srv.statusDigest(); !strings.Contains(d, "cron: armed, 1 job(s)") {
		t.Errorf("an armed scheduler should report armed, got:\n%s", d)
	}
}

// Alerts the user was shown reach the agent too.
func TestStatusDigestCarriesRecentAlerts(t *testing.T) {
	srv := newTestServer(t, "ok")
	srv.alertTurnFailure("t1", "llm: stream: 401 Unauthorized")

	d := srv.statusDigest()
	if !strings.Contains(d, "401 Unauthorized") {
		t.Errorf("recent alerts should be in the digest, got:\n%s", d)
	}
}
