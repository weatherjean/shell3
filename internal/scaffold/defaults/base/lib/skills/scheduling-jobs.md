You can give yourself recurring background work with cron jobs. A job fires on a
schedule and dispatches a SUBAGENT with a prompt; the result is posted to chat.
Cron is a Telegram-host feature, so jobs live INSIDE the `shell3.telegram{}`
block under a `cron = { ... }` key (a flat list of jobs — no `jobs=` wrapper).

## The block
shell3.telegram({
  token = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
  chat_id = "...", agent = "code",
  cron = {
    { name="nightly", schedule="0 9 * * *", agent="explorer",
      prompt="Summarize anything noteworthy.", notify=true },
  },
})

## Fields
- name: identifier for /run <name> and the dashboard.
- schedule: 5-field cron "min hour dom mon dow", or @hourly/@daily/@weekly,
  or @every 30s / @every 5m / @every 1h.
- agent: MUST be a declared shell3.subagent (e.g. explorer), NOT a top-level agent.
- prompt: the instruction handed to the subagent.
- workdir: optional working directory.
- notify: true posts the result to chat; false runs quietly (errors still post).

## Arming and testing
1. Edit shell3.lua to add/adjust the job.
2. Call the `reload` tool to validate and arm it (a bad schedule or unknown
   subagent is rejected; the old config keeps running).
3. Fire on demand with /run <name>; or wait for the schedule.
Removing a job and reloading disarms it.
