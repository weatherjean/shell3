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

The image comes back base64 on the reply message; the saved file's extension
follows the returned media type (png/jpg/webp), and `size` is ignored on this
shape. Two OpenRouter endpoints are deliberately not used: the dedicated
`/api/v1/images` endpoint pre-authorizes worst-case cost (~$2 for a Gemini
image model) and 402s on any lower balance, and the `/api/v1/videos`
video-generation endpoint is an async job API shell3 doesn't wire up.
