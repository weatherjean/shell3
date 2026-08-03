# Voice + images

Four optional blocks under `media:` in `shell3.yaml` — `stt`, `tts`,
`describe`, `imagegen` — each point at a model by name. The scaffold ships
them as commented hints (except `describe`, which boot writes live when you
answer that your model has vision). Full reference:
[configuration.md](../configuration.md#voice--images--media).

## Groq quickstart (one free key, STT + TTS)

Groq's free tier serves an OpenAI-compatible transcription model and a
text-to-speech model, so one key covers voice in and out:

```yaml
models:
  groq-whisper:
    base_url: https://api.groq.com/openai/v1
    api_key: env:GROQ_API_KEY
    model: whisper-large-v3-turbo
  groq-tts:
    base_url: https://api.groq.com/openai/v1
    api_key: env:GROQ_API_KEY
    model: playai-tts

media:
  stt: { model: groq-whisper }                    # dictation → text
  tts: { model: groq-tts, voice: Fritz-PlayAI }
```

Add `GROQ_API_KEY=...` to `.env` and `/reload`: a voice note you send is
transcribed and becomes the message, and `/voice inbound` (or `always`) makes
the reply come back as a voice note. Without `stt`, a voice note is only saved
to disk; without `tts`, replies stay text.

## OpenRouter variant (one key for STT + TTS + describe)

OpenRouter also serves OpenAI-compatible `/audio/transcriptions` and
`/audio/speech`, so a single OpenRouter key covers voice in/out **and** the
image `describe` fallback. One caveat: OpenRouter's TTS emits `mp3`/`pcm`
only (no opus), so a spoken reply arrives as an audio file rather than a
Telegram voice bubble:

```yaml
models:
  or-whisper:
    base_url: https://openrouter.ai/api/v1
    api_key: env:OPENROUTER_API_KEY
    model: openai/whisper-1
  or-tts:
    base_url: https://openrouter.ai/api/v1
    api_key: env:OPENROUTER_API_KEY
    model: hexgrad/kokoro-82m
  or-vision:
    base_url: https://openrouter.ai/api/v1
    api_key: env:OPENROUTER_API_KEY
    model: openai/gpt-4o-mini

media:
  stt: { model: or-whisper }
  tts: { model: or-tts, voice: af_bella, format: mp3, mode: inbound }
  describe: { model: or-vision }   # only if your main model can't see images
```

OpenRouter doesn't serve the OpenAI `images/generations` shape — its image
models generate through chat completions with `modalities=["image","text"]`.
`media.imagegen` speaks that dialect via `api: openrouter` — no need for a
different provider:

```yaml
models:
  or-image:
    base_url: https://openrouter.ai/api/v1
    api_key: env:OPENROUTER_API_KEY
    model: google/gemini-2.5-flash-image

media:
  imagegen: { model: or-image, api: openrouter }
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

```yaml
media:
  describe: { model: some-vision-model }   # only if your main model can't see images
  imagegen: { model: some-image-model, size: 1024x1024 }
```

`describe` captions an inbound photo before the turn, for a **text-only** main
model — a vision-capable one reads the file itself with `read_media`. `imagegen` is
one declaration, every agent: the main agent **and each subagent** get an
`image_generate{prompt, size?}` tool. It saves the image to `~/.shell3/media/`
and returns the path; the main agent delivers it with `send_media_telegram`
(kind `photo`), and a subagent includes the path in its report so the main
agent can send it. Want to keep a subagent from
generating? Gate it in that subagent's hook script like any other tool
(`name` is `image_generate`; `headless` is true for subagents and cron jobs).

All media — everything you send the bot (`tg-*`), generated images (`img-*`),
and synthesized speech (`tts-*`, cached so re-speaking a reply costs nothing) —
lives in `~/.shell3/media/`, so everything the agent has made or heard keeps a
stable path: re-readable with `read_media` and re-sendable with
`send_media_telegram`. It grows until you prune it.

## Reading PDFs and video: `read_media`

`read_media` (needs `media` in the agent's `tools`) also accepts PDFs (`.pdf`, up
to 20 MB) and video (`.mp4`/`.webm`/`.mov`, up to 40 MB), alongside the usual
images and audio. PDFs go over an OpenAI-compatible `file` content part, so
they work against OpenAI or OpenRouter alike. Video goes over a `video_url`
part — an OpenRouter/Gemini extension to the chat-completions dialect — so it
only works with a model/provider that accepts it (e.g. Gemini via
OpenRouter); a plain OpenAI endpoint will reject a video attachment. Note
OpenRouter additionally requires **at least $1.00 of account balance** for
any request carrying video, regardless of its actual cost.
