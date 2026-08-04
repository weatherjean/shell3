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

Add `GROQ_API_KEY=...` to `.env`, reload, and reopen the interface: the
composer's microphone records a message and fills in the transcript for you to
edit before sending, and the read-aloud control on a reply speaks it back.
Without these two blocks both controls fall back to the browser's own Web
Speech APIs.

## OpenRouter variant (one key for STT + TTS + describe)

OpenRouter also serves OpenAI-compatible `/audio/transcriptions` and
`/audio/speech`, so a single OpenRouter key covers voice in/out **and** the
image `describe` fallback. One caveat: OpenRouter's TTS emits `mp3`/`pcm`
only (no opus) — fine in a browser, which plays whatever comes back:

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
  tts: { model: or-tts, voice: af_bella, format: mp3 }
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

`describe` captions an image before a turn, for a **text-only** main model —
a vision-capable one reads the file itself with `read_media`. `imagegen` is
one declaration, every agent: the main agent **and each subagent** get an
`image_generate{prompt, size?}` tool. It saves the image to `~/.shell3/media/`
and returns the path; the main agent is told to show it by writing a markdown
image at its `/api/media/<file>` URL, and a subagent to include the path in
its report so the main agent can deliver it. Want to keep a subagent from
generating? Gate it in that subagent's hook script like any other tool
(`name` is `image_generate`; `headless` is true for subagents and cron jobs).

All media — dictated recordings (`web-*`), uploads (`up-*`), generated images
(`img-*`), and synthesized speech (`tts-*`, cached so replaying a reply costs
nothing) — lives in `~/.shell3/media/`, so everything the agent has made or
heard keeps a stable path: re-readable with `read_media`, servable to the
browser at `/api/media/<file>`, and browsable in the interface's **Files**
view, which carries the media folder as a second root beside the config tree
(newest first, images and audio previewed inline). It grows until you prune
it.

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
