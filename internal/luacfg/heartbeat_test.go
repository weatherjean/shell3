package luacfg

import (
	"strings"
	"testing"
	"time"
)

// hbConfig wraps a shell3.heartbeat block in a minimal valid config.
func hbConfig(block string) string {
	return `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
` + block
}

func TestLoadHeartbeat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", hbConfig(`
shell3.heartbeat({
  every = "30m",
  checklist = [[- check the mail
- check the calendar]],
  active = { from = "08:00", to = "23:00", tz = "Europe/Berlin" },
})
`))
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hb := c.Heartbeat()
	if hb == nil {
		t.Fatal("want heartbeat config, got nil")
	}
	if hb.Every != 30*time.Minute {
		t.Fatalf("want 30m, got %v", hb.Every)
	}
	if !strings.Contains(hb.Checklist, "check the calendar") {
		t.Fatalf("bad checklist: %q", hb.Checklist)
	}
	if hb.ActiveFrom != "08:00" || hb.ActiveTo != "23:00" || hb.TZ != "Europe/Berlin" {
		t.Fatalf("bad active hours: %+v", hb)
	}
}

func TestLoadHeartbeatAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", hbConfig(""))
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Heartbeat() != nil {
		t.Fatal("want nil heartbeat when not declared")
	}
}

func TestLoadHeartbeatDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", hbConfig(`
shell3.heartbeat({ every = "1h", checklist = "- anything urgent?" })
`))
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hb := c.Heartbeat()
	if hb.Every != time.Hour {
		t.Fatalf("want 1h, got %v", hb.Every)
	}
	// No active block: always active, host-local tz.
	if hb.ActiveFrom != "" || hb.ActiveTo != "" || hb.TZ != "" {
		t.Fatalf("want empty active hours, got %+v", hb)
	}
	if hb.Prompt != "" {
		t.Fatalf("want empty prompt override, got %q", hb.Prompt)
	}
}

func TestLoadHeartbeatErrors(t *testing.T) {
	cases := []struct {
		name, block, wantErr string
	}{
		{"missing every", `shell3.heartbeat({ checklist = "x" })`, "every"},
		{"bad every", `shell3.heartbeat({ every = "soon", checklist = "x" })`, "every"},
		{"zero every", `shell3.heartbeat({ every = "0m", checklist = "x" })`, "every"},
		{"missing checklist", `shell3.heartbeat({ every = "30m" })`, "checklist"},
		{"blank checklist", `shell3.heartbeat({ every = "30m", checklist = "  \n " })`, "checklist"},
		{"unknown key", `shell3.heartbeat({ every = "30m", checklist = "x", nope = 1 })`, `unknown key "nope"`},
		{"unknown active key", `shell3.heartbeat({ every = "30m", checklist = "x", active = { from = "08:00", to = "22:00", zone = "UTC" } })`, `unknown key "zone"`},
		{"active missing to", `shell3.heartbeat({ every = "30m", checklist = "x", active = { from = "08:00" } })`, "active"},
		{"bad active time", `shell3.heartbeat({ every = "30m", checklist = "x", active = { from = "8am", to = "22:00" } })`, "HH:MM"},
		{"bad tz", `shell3.heartbeat({ every = "30m", checklist = "x", active = { from = "08:00", to = "22:00", tz = "Mars/Olympus" } })`, "tz"},
		{"second declaration", `shell3.heartbeat({ every = "30m", checklist = "x" })
shell3.heartbeat({ every = "1h", checklist = "y" })`, "one shell3.heartbeat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "shell3.lua", hbConfig(tc.block))
			_, err := Load(dir + "/shell3.lua")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
