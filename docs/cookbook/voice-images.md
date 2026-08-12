# Voice + images

There is no `media:` config block. shell3's verbs are bash; a capability
that shells out to an API doesn't need one — transcription, speech, and
image generation are wrapper scripts you write once and drop into
`~/.shell3/lib/bin/`, the same pattern as any other tool
([Scripts & secrets](../configuration.md#scripts--secrets)). What ships
built in: `read_media` (the agent opens an image/audio/PDF/video file
itself when it decides to) and `send_media_telegram` (the agent sends a
file back). Everything below is the glue in between.

**Transcription is agent-discretion, not automatic.** There is no preflight
that transcribes every inbound voice note before the turn starts. The agent
sees the attachment path in the prompt and decides whether to run `stt.sh`
on it — usually yes for a voice note, not for a photo it can already read
with `read_media`. Tell it when, with the skill at the bottom of this page.

## `stt.sh` — transcribe an inbound voice note

Takes an audio file path, prints the transcript to stdout.

**Local, via whisper.cpp** (no network, no per-call cost; build
[whisper.cpp](https://github.com/ggml-org/whisper.cpp) first and point
`WHISPER_BIN`/`WHISPER_MODEL` at your build):

```bash
#!/usr/bin/env bash
# stt.sh <audio-file> — transcribe with a local whisper.cpp build
set -euo pipefail
audio="${1:?usage: stt.sh <audio-file>}"
bin="${WHISPER_BIN:-$HOME/whisper.cpp/build/bin/whisper-cli}"
model="${WHISPER_MODEL:-$HOME/whisper.cpp/models/ggml-base.en.bin}"
"$bin" -m "$model" -f "$audio" -nt --output-txt --output-file /tmp/stt-out >/dev/null
cat /tmp/stt-out.txt
rm -f /tmp/stt-out.txt
```

**Remote, via an OpenAI-compatible `/audio/transcriptions` endpoint** (Groq's
free tier, OpenAI, OpenRouter — any provider serving that shape). The key is
read from `.env` at point of use and never touches the conversation:

```bash
#!/usr/bin/env bash
# stt.sh <audio-file> — transcribe via an OpenAI-compatible endpoint
set -euo pipefail
audio="${1:?usage: stt.sh <audio-file>}"
key="$(grep '^GROQ_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
curl -fsS --max-time 30 https://api.groq.com/openai/v1/audio/transcriptions \
  -H "Authorization: Bearer $key" \
  -F "model=whisper-large-v3-turbo" \
  -F "file=@${audio}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["text"])'
```

Install either as `~/.shell3/lib/bin/stt.sh`, `chmod +x`. The agent runs it
as `~/.shell3/lib/bin/stt.sh /path/to/voice.ogg` and gets the transcript back
as tool output — no separate injection mechanism, no `echo` config, no
`media:` block to wire it into.

## `say.sh` — speak a reply

Takes text on stdin, writes an ogg/opus file. This matters for Telegram
specifically: it renders an **ogg/opus** file as a voice bubble and anything
else (mp3, wav, aiff) as a plain audio-file attachment — so the ffmpeg
transcode step is not optional if you want the voice-bubble UI.

```bash
#!/usr/bin/env bash
# say.sh — text on stdin -> ogg/opus on stdout path (prints the path)
set -euo pipefail
out="$(mktemp -t say-XXXXXX).ogg"
text="$(cat)"

if command -v say >/dev/null; then
  # macOS
  tmp="$(mktemp -t say-XXXXXX).aiff"
  say -o "$tmp" "$text"
  ffmpeg -y -loglevel error -i "$tmp" -c:a libopus "$out"
  rm -f "$tmp"
elif command -v espeak-ng >/dev/null; then
  # Linux: apt install espeak-ng ffmpeg
  tmp="$(mktemp -t say-XXXXXX).wav"
  espeak-ng -w "$tmp" "$text"
  ffmpeg -y -loglevel error -i "$tmp" -c:a libopus "$out"
  rm -f "$tmp"
else
  echo "say.sh: need macOS 'say' or 'espeak-ng'" >&2
  exit 1
fi

echo "$out"
```

Install as `~/.shell3/lib/bin/say.sh`, `chmod +x`. The agent runs
`echo "$reply" | ~/.shell3/lib/bin/say.sh`, gets a path back, and delivers it
with the built-in `send_media_telegram` tool (kind `voice`) — no `tts:`
block, no `/voice` mode, no persisted state.

A remote TTS variant (OpenAI-compatible `/audio/speech`) follows the same
shape as `stt.sh` above: `curl` the endpoint with the key from `.env`, write
the response bytes to a file, and if the provider doesn't emit opus directly
(OpenAI's `/audio/speech` doesn't; Groq's does), pipe the result through the
same `ffmpeg -c:a libopus` step before handing the path to
`send_media_telegram`.

## `imagegen.sh` — generate an image

```bash
#!/usr/bin/env bash
# imagegen.sh <prompt> — generate an image, print the saved path
set -euo pipefail
prompt="${1:?usage: imagegen.sh <prompt>}"
key="$(grep '^OPENAI_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
out="$(mktemp -t img-XXXXXX).png"

curl -fsS --max-time 60 https://api.openai.com/v1/images/generations \
  -H "Authorization: Bearer $key" \
  -H "Content-Type: application/json" \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"model":"gpt-image-1","prompt":sys.argv[1],"size":"1024x1024"}))' "$prompt")" \
  | python3 -c 'import json,sys,base64; d=json.load(sys.stdin)["data"][0]["b64_json"]; sys.stdout.buffer.write(base64.b64decode(d))' \
  > "$out"

echo "$out"
```

Install as `~/.shell3/lib/bin/imagegen.sh`, `chmod +x`. The agent runs
`~/.shell3/lib/bin/imagegen.sh "a red bicycle"`, gets a path back, and
delivers it with `send_media_telegram` (kind `photo`). There is no
`image_generate` tool anymore — this script is the whole replacement, and
because it's a script you control the provider, the model, and the prompt
shape directly instead of going through a fixed two-provider dispatcher.

OpenRouter's chat-completions image dialect works the same way if you'd
rather not use OpenAI: POST to `chat/completions` with
`"modalities": ["image", "text"]` and a model like
`google/gemini-2.5-flash-image`, then base64-decode the image out of the
reply instead of a `data[0].b64_json` field. Its dedicated `/api/v1/images`
endpoint is worth avoiding — it pre-authorizes worst-case cost (~$2 per
call) and 402s on lower balances; the chat-completions route bills actual
usage.

## `skills/media.md` — tell the agent when to use them

A skill is what makes the agent actually reach for these scripts instead of
ignoring an attachment. This one is yours to write — none of these scripts
ship with shell3, so a skill referencing them only makes sense once you've
installed the ones you want:

```markdown
---
name: media
description: Use when a chat message includes an audio attachment (transcribe it before answering) or when a spoken/image reply would serve the user better than text.
---

# Voice and images

- An inbound `audio/*` attachment: run
  `~/.shell3/lib/bin/stt.sh <path>` and treat the transcript as what the
  user said. Do this before answering — the raw audio path alone is not
  useful context. Skip it for very short clips only if the surrounding
  message already makes the intent clear.
- An inbound image: `read_media <path>` — no separate captioning step,
  the main model looks at it directly.
- A reply worth speaking (the user asked out loud, or asked for voice):
  pipe the reply text through `~/.shell3/lib/bin/say.sh` and send the
  resulting file with `send_media_telegram` (kind `voice`).
- A reply that needs a generated image: `~/.shell3/lib/bin/imagegen.sh
  "<prompt>"` and send the result with `send_media_telegram` (kind `photo`).
```

Drop it in `~/.shell3/skills/media.md`, run `shell3 health`, `/reload`.

## Gating

These are ordinary scripts run through `bash`/`bash_bg` — the tool-call hook
sees them like any other command (`command` carries the full invocation), so
if you want to gate image generation or paid transcription calls
separately from everything else, match on `stt.sh`/`say.sh`/`imagegen.sh` by
name in `hooks/tool-call.sh`. See
[configuration.md](../configuration.md#the-command-gate--hookssh) for the
hook's payload shape.
