package config

import (
	"testing"
	"time"
)

func TestParseCronFile(t *testing.T) {
	j, err := parseCronFile([]byte("---\nschedule: \"@daily\"\nagent: explorer\nnotify: true\n---\nSummarize the day.\n"), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "daily" || j.Schedule != "@daily" || j.Agent != "explorer" || !j.Notify {
		t.Fatalf("job = %+v", j)
	}
	if j.Prompt != "Summarize the day.\n" {
		t.Fatalf("prompt = %q", j.Prompt)
	}
}

func TestParseCronFileErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no schedule":  "---\nagent: a\n---\nbody\n",
		"no agent":     "---\nschedule: \"@daily\"\n---\nbody\n",
		"no body":      "---\nschedule: \"@daily\"\nagent: a\n---\n",
		"unknown key":  "---\nschedule: \"@daily\"\nagent: a\nprompt: inline\n---\nbody\n",
		"bad schedule": "---\nschedule: every 5 min\nagent: a\n---\nbody\n",
	} {
		if _, err := parseCronFile([]byte(in), "x"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseHeartbeatFile(t *testing.T) {
	hb, err := parseHeartbeatFile([]byte("---\nevery: 30m\nactive: { from: \"08:00\", to: \"23:00\", tz: Europe/Berlin }\n---\n- check inbox\n"))
	if err != nil {
		t.Fatal(err)
	}
	if hb.Every != 30*time.Minute || hb.ActiveFrom != "08:00" || hb.TZ != "Europe/Berlin" {
		t.Fatalf("hb = %+v", hb)
	}
	if hb.Checklist != "- check inbox\n" {
		t.Fatalf("checklist = %q", hb.Checklist)
	}
}

func TestParseHeartbeatErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no every":    "---\n{}\n---\nbody\n",
		"bad every":   "---\nevery: soon\n---\nbody\n",
		"no body":     "---\nevery: 5m\n---\n",
		"half window": "---\nevery: 5m\nactive: { from: \"08:00\" }\n---\nbody\n",
		"bad hhmm":    "---\nevery: 5m\nactive: { from: \"8am\", to: \"23:00\" }\n---\nbody\n",
		"bad tz":      "---\nevery: 5m\nactive: { from: \"08:00\", to: \"23:00\", tz: Mars/Olympus }\n---\nbody\n",
	} {
		if _, err := parseHeartbeatFile([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
