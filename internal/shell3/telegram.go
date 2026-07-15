package shell3

import "github.com/weatherjean/shell3/internal/agentsetup"

// TelegramConfig mirrors the parsed shell3.telegram{} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig mirrors the parsed shell3.telegram.dashboard{} block.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
	Tunnel  string
}

// CronJob mirrors one parsed cron job (top-level shell3.cron list).
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// Telegram returns the shell3.telegram{} config the Runtime was built with
// (zero value when the config declares none).
func (rt *Runtime) Telegram() TelegramConfig { return rt.telegram }

// Cron returns the cron jobs declared via top-level shell3.cron{...}.
func (rt *Runtime) Cron() []CronJob { return rt.cron }

// telegramFromParts maps the agentsetup/luacfg telegram config onto the shell3
// TelegramConfig the Runtime carries. Shared by NewRuntime and Reload so the
// two build the same value.
func telegramFromParts(p *agentsetup.Parts) TelegramConfig {
	tg := p.Telegram()
	return TelegramConfig{
		Token: tg.Token, ChatID: tg.ChatID, WorkDir: tg.WorkDir,
		Dashboard: DashboardConfig{
			Enabled: tg.Dashboard.Enabled, Addr: tg.Dashboard.Addr,
			URL: tg.Dashboard.URL, Tunnel: tg.Dashboard.Tunnel,
		},
	}
}

// cronFromParts maps the parsed cron jobs onto the shell3 CronJob slice.
func cronFromParts(p *agentsetup.Parts) []CronJob {
	var jobs []CronJob
	for _, j := range p.Cron() {
		jobs = append(jobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
	return jobs
}
