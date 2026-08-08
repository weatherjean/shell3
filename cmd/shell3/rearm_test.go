//go:build unix

package main

import (
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

type nopDispatcher struct{}

func (nopDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	return "sub1", nil
}

// The user's exact repro: no cron/ files at startup (no scheduler at all), a
// job added later, then /reload — it must arm.
func TestRearmCronZeroToSome(t *testing.T) {
	sched, err := rearmCron(nopDispatcher{}, []shell3.CronJob{
		{Name: "leads", Schedule: "@every 30m", Agent: "assistant", Prompt: "tick"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sched == nil {
		t.Fatal("a job list must arm a scheduler even when none existed before")
	}
	defer sched.Stop()
	if jobs := sched.Jobs(); len(jobs) != 1 || jobs[0].Name != "leads" {
		t.Errorf("scheduler jobs = %+v, want the one declared job", jobs)
	}
}

func TestRearmCronSomeToZero(t *testing.T) {
	old, err := rearmCron(nopDispatcher{}, []shell3.CronJob{
		{Name: "leads", Schedule: "@daily", Agent: "assistant", Prompt: "tick"},
	}, nil)
	if err != nil || old == nil {
		t.Fatalf("arming failed: %v", err)
	}
	next, err := rearmCron(nopDispatcher{}, nil, old)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Error("an empty job list must disarm the scheduler")
	}
}

// A bad schedule in a new file must leave the previous scheduler running.
func TestRearmCronBadScheduleKeepsOld(t *testing.T) {
	old, err := rearmCron(nopDispatcher{}, []shell3.CronJob{
		{Name: "leads", Schedule: "@daily", Agent: "assistant", Prompt: "tick"},
	}, nil)
	if err != nil || old == nil {
		t.Fatalf("arming failed: %v", err)
	}
	defer old.Stop()
	got, err := rearmCron(nopDispatcher{}, []shell3.CronJob{
		{Name: "broken", Schedule: "not a schedule", Agent: "assistant", Prompt: "x"},
	}, old)
	if err == nil {
		t.Fatal("a malformed schedule must fail the re-arm")
	}
	if got != old {
		t.Error("on error the OLD scheduler must stay in charge")
	}
}
