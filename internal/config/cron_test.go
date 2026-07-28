package config

import "testing"

func TestParseCronFile(t *testing.T) {
	j, err := parseCronFile([]byte("---\nschedule: \"@daily\"\nagent: explorer\ndirect: true\n---\nSummarize the day.\n"), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "daily" || j.Schedule != "@daily" || j.Agent != "explorer" || !j.Direct {
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
