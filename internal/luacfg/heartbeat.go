package luacfg

import (
	"regexp"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Heartbeat is the parsed shell3.heartbeat{} block: a periodic check-in turn
// injected into the main session while it is idle. Checklist is the standing
// orders the tick prompt carries; the model replies HEARTBEAT_OK when nothing
// needs attention and the host suppresses the message.
type Heartbeat struct {
	Every     time.Duration
	Checklist string
	Prompt    string // preamble override; "" = built-in default
	// ActiveFrom/ActiveTo bound ticking to a daily window ("HH:MM", from
	// inclusive, to exclusive; from > to spans midnight). Both empty = 24/7.
	ActiveFrom string
	ActiveTo   string
	TZ         string // IANA zone for the window; "" = host-local
}

// Heartbeat returns the parsed shell3.heartbeat{} block, nil when not declared.
func (c *LoadedConfig) Heartbeat() *Heartbeat { return c.heartbeat }

var heartbeatKeys = map[string]bool{"every": true, "checklist": true, "prompt": true, "active": true}
var heartbeatActiveKeys = map[string]bool{"from": true, "to": true, "tz": true}

var hhmmRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// luaHeartbeat parses shell3.heartbeat({ every=..., checklist=..., prompt=...,
// active={from=..., to=..., tz=...} }). Exactly one declaration is allowed.
func (c *LoadedConfig) luaHeartbeat(L *lua.LState) int {
	if c.heartbeat != nil {
		L.RaiseError("heartbeat: only one shell3.heartbeat may be declared")
	}
	opts := L.CheckTable(1)
	mustKeys(L, opts, "heartbeat", heartbeatKeys)
	hb := &Heartbeat{
		Checklist: optStr(opts, "checklist"),
		Prompt:    optStr(opts, "prompt"),
	}
	every := optStr(opts, "every")
	if every == "" {
		L.RaiseError("heartbeat: every is required (e.g. \"30m\")")
	}
	d, err := time.ParseDuration(every)
	if err != nil || d <= 0 {
		L.RaiseError("heartbeat: every %q must be a positive duration (e.g. \"30m\", \"1h\")", every)
	}
	hb.Every = d
	if strings.TrimSpace(hb.Checklist) == "" {
		L.RaiseError("heartbeat: checklist is required — the standing orders each tick carries")
	}
	if a, ok := opts.RawGetString("active").(*lua.LTable); ok {
		mustKeys(L, a, "heartbeat.active", heartbeatActiveKeys)
		hb.ActiveFrom = optStr(a, "from")
		hb.ActiveTo = optStr(a, "to")
		hb.TZ = optStr(a, "tz")
		if hb.ActiveFrom == "" || hb.ActiveTo == "" {
			L.RaiseError("heartbeat: active needs both from and to")
		}
		for _, v := range []string{hb.ActiveFrom, hb.ActiveTo} {
			if !hhmmRe.MatchString(v) {
				L.RaiseError("heartbeat: active time %q must be HH:MM (24h)", v)
			}
		}
		if hb.TZ != "" {
			if _, err := time.LoadLocation(hb.TZ); err != nil {
				L.RaiseError("heartbeat: active tz %q is not a valid IANA zone: %s", hb.TZ, err.Error())
			}
		}
	}
	c.heartbeat = hb
	return 0
}
