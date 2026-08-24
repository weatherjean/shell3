# `shell3 serve` — the bring-your-own front-end protocol

`shell3 serve` runs the same agent as `shell3 telegram` — same fresh-turn
threading, host commands, task-report delivery, and
cron — over newline-delimited JSON on stdin/stdout instead of the Telegram
Bot API. Your front-end (a Discord bridge, a custom dashboard backend, a test
harness) spawns the process and translates.

The transport has no port and no auth: owning the subprocess's stdio is
the access model, the parallel of Telegram's `chat_id`. (Serve does start
the same read-only web dash `shell3 telegram` does — `127.0.0.1`,
token-gated, `dash_port: 0` disables.) A remote front-end
needs its own relay; that is deliberately out of scope.

```
your-frontend ──spawn──▶ shell3 serve
      │  stdin: {"type":"message","text":"hi"}
      ▼  stdout: {"type":"send","id":"a…-2","reply_to_id":"…","text":"…"}
```

Running serve *alongside* `shell3 telegram` as a second window onto the same
agent is not supported — run two processes with two config dirs instead.
Serve keeps its own current-conversation marker in the runs store, so the
two front-ends' conversations never cross-resolve.

## Framing

- One JSON object per line, both directions, UTF-8.
- Every object carries `"type"`. Unknown types and malformed lines are
  logged to stderr and ignored, so additive protocol growth won't break a
  front-end.
- stdin EOF (or SIGINT/SIGTERM) shuts the agent down cleanly.
- stderr is the diagnostic channel (startup notes, skipped input); everything
  chat-shaped is a stdout event.

## Message ids and threading

Ids are opaque strings. Your inbound messages carry your own ids (Discord
snowflakes work as-is); if you omit one, the agent assigns it. Outbound
events carry agent-assigned ids (`a<boot>-<n>`, unique across restarts).

Conversation follows the Telegram model: one long-lived conversation per
CHAT, which every `message` continues whether or not it carries
`reply_to_id` — a reply's quoted `reply_to` text is injected as context for
the agent, never a conversation switch. A serve front-end that sends
everything under one chat id therefore behaves exactly as it always did; one
that stamps distinct chat ids gets one conversation each, isolated the way
two Telegram groups are. The `/new` command starts a fresh conversation in the
chat it was sent from; each chat's session id persists on disk, so a restart
resumes where it left off.

One turn runs at a time PER CHAT (bounded across chats by
`telegram.max_concurrent_turns`), but sending always succeeds: a `message`
arriving mid-turn queues silently and the backlog drains as one batched
turn after the turn ends, anchored at the newest message.

## Handshake

The first stdout line:

```json
{"type":"hello","protocol":2,"commands":[{"command":"dash","description":"Open the web dashboard (link valid ~1h)"}, …]}
```

`protocol` bumps only on breaking changes. `commands` is the host-answered
`/` menu (`/dash`, `/stop`, `/superstop`, …) so your front-end can populate
its own command UI. Send a command as a normal `message` whose text starts with
`/` — it is answered host-side with zero tokens.

A removed event kind is not rejected on the wire: an inbound event the host
no longer knows silently hits the "ignoring unknown event type" path with no
response, since tolerating unknown types is meant for additive growth, not
for detecting removals. Check `protocol` on the hello line — it is the only
signal that the wire you were built against has changed.

A greeting `send` follows the hello.

## Client → agent events

```json
{"type":"message","id":"m1","reply_to_id":"","text":"hi","reply_to":"","media":[{"path":"/abs/pic.jpg","mime":"image/jpeg","filename":"pic.jpg"}]}
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
There is no automatic transcription or captioning step for voice notes or
photos — the path goes into the prompt and the agent decides whether to act
on it (via `read_media`, or a wrapper script — see
[cookbook/voice-images.md](cookbook/voice-images.md)).

**`media[].path` must never be user-influenced.** It is read directly at
transport ingest — before the turn, so the tool-call hook never sees it —
which is intended: possessing this process's stdio already means possessing
the agent. But it means a bridge that lets a remote user choose the path
(echoing a supplied attachment location, say) hands that user arbitrary file
reads on this machine. Copy inbound files into a directory you own and pass
your own path.

## Agent → client events

```json
{"type":"send","id":"a…-7","reply_to_id":"m1","text":"reply **markdown**"}
{"type":"media","id":"a…-8","kind":"document","path":"…/serve_out/f…-reply.md","filename":"reply.md","caption":"full reply"}
{"type":"edit","id":"a…-9","text":"progress: reading file…"}
{"type":"typing"}
```

- `send` — a chat bubble. `text` is markdown (plain text is valid markdown);
  Telegram's HTML never appears on the wire. `reply_to_id` threads it; you
  can reply to its `id` to continue the conversation. Long replies chunk at
  4096 bytes, capped at 2 bubbles + a `reply.md` document, exactly as on
  Telegram. Background-completion posts (`🔔 …`, `⏰ <job>: …`) and agent
  mail (`✉️ …`) are ordinary `send` events; under `/quiet` they (and their
  `media` documents) carry `"silent": true`, which a front-end should honor
  by not ringing.
- `media` — a file, by local path (spooled under
  `.shell3_project/serve_out/`). `kind` is
  `photo|voice|audio|video|document`. Only `document` carries an `id`
  (documents advance the thread anchor).
- `edit` — replace the text of a previously sent message (safe to ignore if
  your surface can't edit). The turn progress bubble arrives as a silent
  `send` followed by `edit`s.
- `delete` — remove a previously sent message by `id` (the progress bubble
  cleaning itself up after a finished turn; safe to ignore).
- `typing` — the "typing…" action, refreshed every few seconds during a
  turn. Ignorable.

## Minimal session

```
◀ {"type":"hello","protocol":2,"commands":[…]}
◀ {"type":"send","id":"a1722-1","text":"๑ï shell3 online — …"}
▶ {"type":"message","id":"m1","text":"what's in ~/notes?"}
◀ {"type":"typing"}
◀ {"type":"send","id":"a1722-2","reply_to_id":"m1","text":"Your notes dir has …"}
▶ {"type":"message","id":"m2","reply_to_id":"a1722-2","text":"summarize the first one"}
◀ {"type":"send","id":"a1722-3","reply_to_id":"m2","text":"It says …"}
```

Drive it by hand:

```bash
printf '%s\n' '{"type":"message","text":"/dash"}' | shell3 serve
```
