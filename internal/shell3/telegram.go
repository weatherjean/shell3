package shell3

import (
	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/luacfg"
)

// TelegramConfig, DashboardConfig, and CronJob are the parsed config blocks as
// luacfg produces them. Aliases (not mirrors): the Runtime hands the parsed
// values straight through, so a field added in luacfg is immediately visible
// here — no hand-written copier to forget.
type (
	TelegramConfig  = luacfg.TelegramConfig
	DashboardConfig = luacfg.DashboardConfig
	CronJob         = luacfg.CronJob
)

// sessionConfigFrom adapts Parts.SessionConfig to the Runtime's per-session
// config func. Shared by NewRuntime and Reload so the two build the same
// adapter.
func sessionConfigFrom(parts *agentsetup.Parts) func(SessionOpts) (chat.Config, error) {
	return func(o SessionOpts) (chat.Config, error) {
		return parts.SessionConfig(agentsetup.SessionOptions{
			Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
		})
	}
}

// Telegram returns the shell3.telegram{} config the Runtime was built with
// (zero value when the config declares none).
func (rt *Runtime) Telegram() TelegramConfig { return rt.telegram }

// Cron returns the cron jobs declared via top-level shell3.cron{...}.
func (rt *Runtime) Cron() []CronJob { return rt.cron }
