# Personal Telegram bot

shell3 ships a Telegram front-end that turns the same engine into an always-on
personal agent you talk to from your phone. It's one binary, one config, one
chat — the bot runs a single agent on a host you control, can schedule its own
recurring work, and can even edit and reload its own config while it's running.

It's a way to use shell3 from your phone: scaffold it once, run it on a machine
that stays up, and message it like you'd message a person.

## What you get

- **A bot tied to one chat.** It only ever talks to your numeric chat id —
  nobody else can drive it.
- **The full agent.** `bash`, `edit_file`, background work, web fetch/search,
  and a read-only `explorer` subagent it can delegate to. Same tools as the
  desktop `code` agent, tuned for chat: short, mobile-friendly replies.
- **Durable memory.** The bot keeps a `MEMORY.md` in its working directory and
  reads it at the start of each conversation, so it remembers your preferences
  and setup across chats.
- **Self-evolution.** It can edit its own `shell3.lua` and apply it live with the
  `reload` tool. A failed reload keeps the old config running, so it can't brick
  itself.
- **Scheduled jobs (cron).** Recurring background tasks that dispatch a subagent
  and post results back to chat — see below.
- **An optional dashboard.** A read-only Mini App showing sessions, usage, and
  scheduled jobs.

## Setup

```sh
shell3 boot --telegram     # scaffold a host config under ~/.shell3/telegram/
```

This writes a `shell3.lua` tuned for chat, the `lib/` modules, and a `.env`
template. You'll need two things in that `.env` (beside the config, never
committed):

- `TELEGRAM_BOT_TOKEN` — create a bot with [@BotFather](https://t.me/BotFather)
  and copy the token.
- your model API key (whatever key your model block references).

Then set your numeric `chat_id` in the config — message
[@userinfobot](https://t.me/userinfobot) to find it. The relevant block looks
like this:

```lua
shell3.telegram({
  token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
  chat_id = "000000000",            -- your numeric id
  agent   = "code",                 -- the single agent the bot runs
  workdir = "/abs/path/to/workdir", -- where its tools run
  dashboard = { enabled = false },  -- or enable it (below)
})
```

## Running it

```sh
shell3 telegram     # start the bot; it listens for your chat
```

Run it on a machine that stays up — a home server, a small VM, a Raspberry Pi.
On start it resumes your latest session, installs a one-tap command bar in the
chat (`/stop`, `/reload`, `/clear`), and begins listening.

### Chat commands

| Command | Effect |
|---------|--------|
| `/stop` | Cancel the turn the bot is currently working on |
| `/reload` | Rebuild and apply the config live (validates first) |
| `/clear` | Reset the conversation context |
| `/run <name>` | Fire a scheduled job by name, right now |

## Scheduled jobs (cron)

Cron is a Telegram-host feature, so jobs live **inside** the `shell3.telegram{}`
block under a `cron = { ... }` key — a flat list of jobs. Each job fires on a
schedule and dispatches a **subagent** (not a top-level agent) with a prompt; the
result is posted back to chat:

```lua
shell3.telegram({
  token = shell3.env.secret("TELEGRAM_BOT_TOKEN"),
  chat_id = "000000000",
  agent = "code",
  cron = {
    { name="prs",   schedule="0 9 * * *", agent="explorer", notify=true,
      prompt="Summarize my open PRs and anything that needs review today." },
    { name="tests", schedule="@hourly",   agent="explorer", notify=false,
      prompt="Run the test suite; if anything fails, summarize the failure." },
  },
})
```

Field reference:

- **name** — identifier for `/run <name>` and the dashboard.
- **schedule** — a 5-field cron expression (`min hour dom mon dow`), or a macro
  (`@hourly` / `@daily` / `@weekly`), or an interval (`@every 30s`, `@every 5m`,
  `@every 1h`).
- **agent** — must be a declared `shell3.subagent` (e.g. `explorer`), **not** a
  top-level agent.
- **prompt** — the instruction handed to the subagent.
- **workdir** — optional working directory for the job.
- **notify** — `true` posts the result to chat; `false` runs quietly (errors
  still post).

To arm a job: edit the config and call `reload` (or `/reload`). A bad schedule or
an unknown subagent is rejected and the old config keeps running. Fire a job on
demand with `/run <name>`. Remove a job and reload to disarm it.

## The dashboard (optional)

Enable a read-only Mini App that shows sessions, token usage, and scheduled jobs:

```lua
dashboard = {
  enabled = true,
  addr    = "127.0.0.1:8765",
  url     = "https://HOST.TAILNET.ts.net/",
},
```

The dashboard binds to localhost. Expose it privately over your tailnet rather
than the public internet:

```sh
tailscale serve https / proxy 127.0.0.1:8765
```

shell3 points the bot's menu button at `url`, so opening the Mini App from the
menu passes Telegram's signed `initData` and authenticates automatically.

## Safety note

The Telegram bot runs model-chosen shell commands on the host, unattended, the
same as any shell3 session — it is **unsafe by default**. Run it as a dedicated
user, in a container, or on a throwaway machine, and gate commands with
`wrap_bash` if you need to. See [security.md](security.md).
