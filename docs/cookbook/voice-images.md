# Voice + images over Telegram

Four optional top-level blocks — `shell3.stt`, `shell3.tts`, `shell3.describe`,
`shell3.imagegen` — each point at a `shell3.model` by name. None are declared
by the base scaffold; the block below is a commented-out starting point at
the bottom of the model section in `shell3.lua`. Full reference:
[configuration.md](../configuration.md#voice--images--shell3stt--tts--describe--imagegen).

## Groq quickstart (one free key, STT + TTS)

Groq's free tier serves an OpenAI-compatible transcription model and a
text-to-speech model, so one key covers voice in and out:

```lua
shell3.model("groq-whisper", { base_url = "https://api.groq.com/openai/v1",
  api_key = shell3.env.secret("GROQ_API_KEY"), model = "whisper-large-v3-turbo" })
shell3.model("groq-tts", { base_url = "https://api.groq.com/openai/v1",
  api_key = shell3.env.secret("GROQ_API_KEY"), model = "playai-tts" })

shell3.stt{ model = "groq-whisper" }                              -- voice notes → text
shell3.tts{ model = "groq-tts", voice = "Fritz-PlayAI", mode = "inbound" }
```

Add `GROQ_API_KEY=...` to `.env`, `/reload`, and send the bot a voice note —
it replies with a `📝 "…"` transcript, runs the turn, and (because `mode =
"inbound"`) speaks the reply back as a voice note. Switch modes any time with
`/voice off|inbound|always`.

## OpenRouter variant (one key for STT + TTS + describe)

OpenRouter also serves OpenAI-compatible `/audio/transcriptions` and
`/audio/speech`, so a single OpenRouter key covers voice in/out **and** the
image `describe` fallback. One caveat: OpenRouter's TTS emits `mp3`/`pcm`
only (no opus), so spoken replies arrive as audio files rather than round
Telegram voice bubbles:

```lua
shell3.model("or-whisper", { base_url = "https://openrouter.ai/api/v1",
  api_key = shell3.env.secret("OPENROUTER_API_KEY"), model = "openai/whisper-1" })
shell3.model("or-tts", { base_url = "https://openrouter.ai/api/v1",
  api_key = shell3.env.secret("OPENROUTER_API_KEY"), model = "hexgrad/kokoro-82m" })
shell3.model("or-vision", { base_url = "https://openrouter.ai/api/v1",
  api_key = shell3.env.secret("OPENROUTER_API_KEY"), model = "openai/gpt-4o-mini" })

shell3.stt{ model = "or-whisper" }
shell3.tts{ model = "or-tts", voice = "af_bella", format = "mp3", mode = "inbound" }
shell3.describe{ model = "or-vision" }   -- only if your main model can't see images
```

OpenRouter doesn't serve the OpenAI `images/generations` shape — its image
models generate through chat completions with `modalities=["image","text"]`.
`shell3.imagegen{}` speaks that dialect via `api = "openrouter"` — no need
for a different provider:

```lua
shell3.model("or-image", { base_url = "https://openrouter.ai/api/v1",
  api_key = shell3.env.secret("OPENROUTER_API_KEY"), model = "google/gemini-2.5-flash-image" })

shell3.imagegen{ model = "or-image", api = "openrouter" }
```

The image comes back base64 on the reply message, and the saved file's
extension follows the returned media type (png/jpg/webp) rather than a fixed
`.png`; `size` is ignored on this shape (the chat route has no size
parameter). (Two things deliberately *not* used: OpenRouter's dedicated
`/api/v1/images` endpoint, which pre-authorizes the request's worst-case cost
— ~$2 for a Gemini image model — and 402s on any lower balance, and its
*video*-generation endpoint `/api/v1/videos`, an async job API that isn't
wired up — not a current feature.)

## Images: describe in, generate out

```lua
shell3.describe{ model = "some-vision-model" }   -- only if your main model can't see images
shell3.imagegen{ model = "some-image-model", size = "1024x1024" }
```

`describe` is only useful for a **text-only** main model — a vision-capable
one already sees an inbound photo directly. `imagegen` is one declaration,
every agent: the main agent **and each subagent** get an
`image_generate{prompt, size?}` tool under every front-end (telegram, web,
dev). It saves the image to `~/.shell3/media/` and returns the path; a
subagent is told to include the path in its report so the main agent can
deliver it. Want to keep a subagent from generating? Gate it in
`shell3.on_tool_call` like any other tool (`t.name == "image_generate"`,
`t.headless` is true for subagents and cron jobs).

All media — inbound Telegram uploads (`tg-*`) and generated images (`img-*`)
— lives in `~/.shell3/media/`, so everything the agent has seen or made keeps
a stable path: re-readable with `read_media`, re-sendable, browsable from the
dashboard file explorer. It grows until you prune it. (Synthesized voice
replies are the exception — sent and deleted immediately.)

## Delivering files back: `kind`

Under Telegram, `send_media_telegram` (registered whenever the bot runs, e.g.
to hand back a generated image) takes an optional `kind`: `"photo"`
(Telegram recompresses to ~1280px), `"voice"` (`.ogg`/`.opus` only), `"audio"`,
`"video"` (`.mp4`/`.webm`/`.mov` only), or `"document"` (default — pixel-exact,
no recompression). Use `"document"` for anything where fidelity matters, e.g.
a screenshot with small text.

## Reading PDFs and video: `read_media`

`read_media` (needs `tools = { media = true }`) also accepts PDFs (`.pdf`, up
to 20 MB) and video (`.mp4`/`.webm`/`.mov`, up to 40 MB), alongside the usual
images and audio. PDFs go over an OpenAI-compatible `file` content part, so
they work against OpenAI or OpenRouter alike. Video goes over a `video_url`
part — an OpenRouter/Gemini extension to the chat-completions dialect — so it
only works with a model/provider that accepts it (e.g. Gemini via
OpenRouter); a plain OpenAI endpoint will reject a video attachment. Note
OpenRouter additionally requires **at least $1.00 of account balance** for
any request carrying video, regardless of its actual cost.
