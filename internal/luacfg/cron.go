package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// CronJob is one parsed cron job entry (shell3.cron list).
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// Cron returns the parsed cron jobs (from shell3.cron{...}).
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

var cronJobKeys = map[string]bool{
	"name": true, "schedule": true, "agent": true, "prompt": true, "workdir": true, "notify": true,
}

// luaCron registers scheduled jobs: shell3.cron({ {name=..., schedule=...,
// agent=..., prompt=..., workdir=..., notify=true}, ... }). Each job dispatches
// a declared subagent on its schedule; the scheduler runs inside
// `shell3 telegram`. Multiple calls append.
func (c *LoadedConfig) luaCron(L *lua.LState) int {
	c.parseCronJobs(L, L.CheckTable(1))
	return 0
}

func (c *LoadedConfig) parseCronJobs(L *lua.LState, jobsT *lua.LTable) {
	n := 0
	jobsT.ForEach(func(_, v lua.LValue) {
		jt, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		n++
		mustKeys(L, jt, "cron.job", cronJobKeys)
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
