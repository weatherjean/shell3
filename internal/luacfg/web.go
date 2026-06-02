package luacfg

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

var webKeys = map[string]bool{
	"host": true, "port": true, "password": true,
	"cookie_ttl": true, "allowed_origins": true,
}

func (c *LoadedConfig) luaWeb(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "web", webKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	w := WebConfig{
		Set:      true,
		Host:     optStr(opts, "host"),
		Port:     optInt(opts, "port"),
		Password: optStr(opts, "password"),
	}
	if ttl := optStr(opts, "cookie_ttl"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			L.RaiseError("web: invalid cookie_ttl %q: %v", ttl, err)
		}
		w.CookieTTL = d
	}
	if ao, ok := opts.RawGetString("allowed_origins").(*lua.LTable); ok {
		w.AllowedOrigins = stringList(ao)
	}
	c.Web = w
	return 0
}
