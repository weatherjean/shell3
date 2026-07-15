package luacfg

import (
	lua "github.com/yuin/gopher-lua"
)

// TelegramConfig is the parsed shell3.telegram{...} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig is the parsed shell3.telegram.dashboard{} block. Tunnel, if
// set, is a shell command spawned at bot start ({addr} replaced by Addr) whose
// output is scanned for the dashboard's public https URL; URL, if set, is the
// fixed public address and wins over a scanned one.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
	Tunnel  string
}

// Telegram returns the parsed shell3.telegram{} block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }

var telegramKeys = map[string]bool{"token": true, "chat_id": true, "workdir": true, "dashboard": true}
var telegramDashboardKeys = map[string]bool{"enabled": true, "addr": true, "url": true, "tunnel": true}

func (c *LoadedConfig) luaTelegram(L *lua.LState) int {
	opts := L.CheckTable(1)
	mustKeys(L, opts, "telegram", telegramKeys)
	tg := TelegramConfig{
		Token:   optStr(opts, "token"),
		ChatID:  optStr(opts, "chat_id"),
		WorkDir: optStr(opts, "workdir"),
	}
	if d, ok := opts.RawGetString("dashboard").(*lua.LTable); ok {
		mustKeys(L, d, "telegram.dashboard", telegramDashboardKeys)
		tg.Dashboard = DashboardConfig{
			Enabled: optBool(d, "enabled"),
			Addr:    optStr(d, "addr"),
			URL:     optStr(d, "url"),
			Tunnel:  optStr(d, "tunnel"),
		}
	}
	c.telegram = tg
	return 0
}
