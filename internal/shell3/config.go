package shell3

import (
	"slices"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
)

// TelegramConfig and CronJob are the parsed config as its owning package
// produces it: the telegram: wiring block from internal/config, and the kit's
// `cron:` declarations from internal/kit. Aliases (not mirrors): the Runtime
// hands the parsed values straight through, so a field added upstream is
// immediately visible here — no hand-written copier to forget.
type (
	TelegramConfig = config.TelegramConfig
	CronJob        = kit.CronJob
)

// sessionConfigFrom adapts Parts.SessionConfig to the Runtime's per-session
// config func. Shared by NewRuntime and Reload so the two build the same
// adapter.
func sessionConfigFrom(parts *agentsetup.Parts) func(SessionOpts) (chat.Config, error) {
	return func(o SessionOpts) (chat.Config, error) {
		return parts.SessionConfig(agentsetup.SessionOptions{
			Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless,
			PromptSuffix: o.PromptSuffix,
		})
	}
}

// Telegram returns the current generation's telegram: config (zero value when
// the config declares none). Locked because Reload swaps Parts under rt.mu.
func (rt *Runtime) Telegram() TelegramConfig {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.parts == nil {
		return TelegramConfig{}
	}
	return rt.parts.Telegram()
}

// Cron returns a snapshot of the kit's `cron:` jobs, in declaration order.
func (rt *Runtime) Cron() []CronJob {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return slices.Clone(rt.cron)
}

// Parts returns the runtime's current shared config assembly, for host code
// that needs config-derived resources Runtime doesn't otherwise expose.
// Locked: races Reload's mu-held swap of the field.
func (rt *Runtime) Parts() *agentsetup.Parts {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.parts
}
