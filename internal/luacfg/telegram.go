package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// CronJob is one parsed cron job entry (shell3.telegram cron list).
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// TelegramConfig is the parsed shell3.telegram{...} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Agent     string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig is the parsed shell3.telegram.dashboard{} block.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}

// Telegram returns the parsed shell3.telegram{} block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }

// Cron returns the parsed cron jobs (from shell3.telegram{ cron = {...} }).
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

var telegramKeys = map[string]bool{"token": true, "chat_id": true, "agent": true, "workdir": true, "dashboard": true, "cron": true}
var telegramDashboardKeys = map[string]bool{"enabled": true, "addr": true, "url": true}

func (c *LoadedConfig) luaTelegram(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "telegram", telegramKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	tg := TelegramConfig{
		Token:   optStr(opts, "token"),
		ChatID:  optStr(opts, "chat_id"),
		Agent:   optStr(opts, "agent"),
		WorkDir: optStr(opts, "workdir"),
	}
	if d, ok := opts.RawGetString("dashboard").(*lua.LTable); ok {
		if err := checkKeys(d, "telegram.dashboard", telegramDashboardKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		tg.Dashboard = DashboardConfig{
			Enabled: optBool(d, "enabled"),
			Addr:    optStr(d, "addr"),
			URL:     optStr(d, "url"),
		}
	}
	// cron jobs are nested under telegram{}: the scheduler is consumed only by
	// the Telegram host, so the config shape reflects that coupling. `cron` is a
	// flat list of job tables (no `jobs=` wrapper).
	if jobsT, ok := opts.RawGetString("cron").(*lua.LTable); ok {
		c.parseCronJobs(L, jobsT)
	}
	c.telegram = tg
	return 0
}

var cronJobKeys = map[string]bool{
	"name": true, "schedule": true, "agent": true, "prompt": true, "workdir": true, "notify": true,
}

func (c *LoadedConfig) parseCronJobs(L *lua.LState, jobsT *lua.LTable) {
	n := 0
	jobsT.ForEach(func(_, v lua.LValue) {
		jt, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		n++
		if err := checkKeys(jt, "cron.job", cronJobKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		job := CronJob{
			Name:     optStr(jt, "name"),
			Schedule: optStr(jt, "schedule"),
			Agent:    optStr(jt, "agent"),
			Prompt:   optStr(jt, "prompt"),
			WorkDir:  optStr(jt, "workdir"),
			Notify:   true, // default
		}
		if v := jt.RawGetString("notify"); v != lua.LNil {
			job.Notify = lua.LVAsBool(v)
		}
		if job.Name == "" {
			job.Name = fmt.Sprintf("job-%d", n)
		}
		c.cron = append(c.cron, job)
	})
}

// validateCron cross-checks the parsed cron jobs after the full config is
// loaded: every job needs a schedule and must reference a declared subagent
// (each tick dispatches a fresh subagent job).
func (c *LoadedConfig) validateCron() error {
	for i := range c.cron {
		if c.cron[i].Schedule == "" {
			return fmt.Errorf("config: cron job %q has no schedule", c.cron[i].Name)
		}
		if c.cron[i].Agent == "" {
			return fmt.Errorf("config: cron job %q has no agent", c.cron[i].Name)
		}
		if _, ok := c.SubagentByName(c.cron[i].Agent); !ok {
			return fmt.Errorf("config: cron job %q references unknown subagent %q", c.cron[i].Name, c.cron[i].Agent)
		}
	}
	return nil
}
