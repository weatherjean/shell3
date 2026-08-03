# `shell3 serve` — the bring-your-own front-end protocol

`shell3 serve` runs the same agent as `shell3 telegram` — same fresh-turn
threading, host commands, hook approvals, notifier completion delivery, and
cron — over newline-delimited JSON on stdin/stdout instead of the Telegram
Bot API. Your front-end (a Discord bridge, a custom dashboard backend, a test
harness) spawns the process and translates.

There is no port, no listener, and no auth: possessing the subprocess's stdio
is the access model, the exact parallel of `chat_id`. A remote front-end needs
its own relay; that is deliberately out of scope.

```
your-frontend ──spawn──▶ shell3 serve
      │  stdin: {"type":"message","text":"hi"}
      ▼  stdout: {"type":"send","id":"a…-2","reply_to_id":"…","text":"…"}
```

Running serve *alongside* `shell3 telegram` as a second window onto the same
agent is not supported — run two processes with two config dirs instead (two
agents with the same brain). Serve keeps its own thread index
(`serve_threads.jsonl`), so the two front-ends' histories never cross-resolve.

## Framing

- One JSON object per line, both directions, UTF-8.
- Every object carries `"type"`. Unknown types and malformed lines are
  logged to stderr and ignored — additive protocol growth won't break you.
- stdin EOF (or SIGINT/SIGTERM) shuts the agent down cleanly.
- stderr is the diagnostic channel (startup notes, skipped input); everything
  chat-shaped is a stdout event.

## Message ids and threading

Ids are opaque strings. Your inbound messages carry your own ids (Discord
snowflakes work as-is); if you omit one, the agent assigns it. Outbound
events carry agent-assigned ids (`a<boot>-<n>`, unique across restarts).

Threading follows the Telegram model: a `message` with no `reply_to_id`
starts a fresh conversation (its own agent session); `reply_to_id` naming any
earlier message id — yours or the agent's — continues that conversation. The
mapping persists on disk, so replies keep working across agent restarts.
Replying to an id the agent doesn't know gets a fixed "can't continue from
that message" send, never a silent new session.

Exactly one turn runs at a time. A `message` arriving mid-turn gets a
courtesy "a turn is running" reply and is dropped, never queued.

## Handshake

The first stdout line:

```json
{"type":"hello","protocol":1,"commands":[{"command":"status","description":"agent, model and MCP health"}, …]}
```

`protocol` bumps only on breaking changes. `commands` is the host-answered
`/` menu (`/status`, `/jobs`, `/stop`, …) so your front-end can populate its
own command UI. Send a command as a normal `message` whose text starts with
`/` — it is answered host-side with zero tokens.

A greeting `send` follows the hello.

## Client → agent events

```json
{"type":"message","id":"m1","reply_to_id":"","text":"hi","reply_to":"","media":[{"path":"/abs/pic.jpg","mime":"image/jpeg","filename":"pic.jpg"}]}
{"type":"callback","id":"cb1","data":"…"}
```

`message` fields:

| field | meaning |
|---|---|
| `id` | your id for this message (optional; assigned when omitted) |
| `reply_to_id` | id this replies to; empty/omitted = fresh conversation |
| `text` | the message text |
| `reply_to` | text of the quoted message, shown to the model (optional) |
| `media` | attachments as local file paths; each `{path, mime, filename}` |

Media files are read from disk by the agent (50 MiB cap per file); an
unreadable entry is skipped with a stderr note and the turn still runs.
Voice notes and photos go through the same STT/describe preflight as on
Telegram when `media:` is configured.

`callback` answers a `confirm` or `menu` event: `data` is the pressed
option's data string, `id` is your id for the press (echoed in the `ack`).

## Agent → client events

```json
{"type":"send","id":"a…-7","reply_to_id":"m1","text":"reply **markdown**"}
{"type":"media","id":"a…-8","kind":"document","path":"…/serve_out/f…-reply.md","filename":"reply.md","caption":"full reply"}
{"type":"menu","id":"a…-9","text":"runs 1/3","options":[{"label":"…","data":"…"}]}
{"type":"confirm","id":"a…-10","text":"Run `rm -rf …`?","yes":"…","no":"…"}
{"type":"edit","id":"a…-10","text":"✅ allowed"}
{"type":"typing"}
{"type":"ack","callback_id":"cb1"}
```

- `send` — a chat bubble. `text` is markdown (plain text is valid markdown);
  Telegram's HTML never appears on the wire. `reply_to_id` threads it; you
  can reply to its `id` to continue the conversation. Long replies chunk at
  4096 bytes, capped at 2 bubbles + a `reply.md` document, exactly as on
  Telegram. Background-completion posts (`🔔 …`, `⏰ <job>: …`) are ordinary
  `send` events.
- `media` — a file, by local path (spooled under
  `.shell3_project/serve_out/`). `kind` is
  `photo|voice|audio|video|document`. Only `document` carries an `id`
  (documents advance the thread anchor).
- `confirm` — a hook `ask` verdict: present Allow/Deny, answer with a
  `callback` whose `data` is the `yes` or `no` string. No answer = the ask's
  fail-safe timeout denies. `edit` then replaces the confirm's text.
- `menu` — a button row (`/runs` paging, `/voice`); answer like a confirm.
- `edit` — replace the text of a previously sent message (safe to ignore if
  your surface can't edit).
- `typing` — the "typing…" action, refreshed every few seconds during a
  turn. Ignorable.
- `ack` — acknowledges a `callback` (Telegram's spinner-stop). Ignorable.

## Minimal session

```
◀ {"type":"hello","protocol":1,"commands":[…]}
◀ {"type":"send","id":"a1722-1","text":"๑ï shell3 online — …"}
▶ {"type":"message","id":"m1","text":"what's in ~/notes?"}
◀ {"type":"typing"}
◀ {"type":"send","id":"a1722-2","reply_to_id":"m1","text":"Your notes dir has …"}
▶ {"type":"message","id":"m2","reply_to_id":"a1722-2","text":"summarize the first one"}
◀ {"type":"send","id":"a1722-3","reply_to_id":"m2","text":"It says …"}
```

Drive it by hand:

```bash
printf '%s\n' '{"type":"message","text":"/status"}' | shell3 serve
```
