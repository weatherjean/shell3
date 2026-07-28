package shell3

import (
	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
)

// TelegramConfig and CronJob are the parsed config blocks as
// internal/config produces them. Aliases (not mirrors): the Runtime hands the parsed
// values straight through, so a field added in internal/config is immediately visible
// here — no hand-written copier to forget.
type (
	TelegramConfig = config.TelegramConfig
	CronJob        = config.CronJob
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

// Telegram returns the telegram: config the Runtime currently holds (zero
// value when the config declares none). Locked because Reload swaps it under
// rt.mu while the front-end reads it: the bot token and chat id are read as
// the bot starts, so an unsynchronised read is a race the reload path would
// eventually lose.
func (rt *Runtime) Telegram() TelegramConfig {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.telegram
}

// Cron returns the jobs declared as cron/<name>.md files.
func (rt *Runtime) Cron() []CronJob { return rt.cron }

// Parts returns the runtime's current shared config assembly, for host code
// that needs config-derived resources Runtime doesn't otherwise expose (e.g.
// building media.Clients from LoadedConfig + EnsureProxy — the media package
// can't be a Runtime dependency since it depends on internal/shell3 itself).
// Locked: races Reload's mu-held swap of the field.
func (rt *Runtime) Parts() *agentsetup.Parts {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.parts
}
