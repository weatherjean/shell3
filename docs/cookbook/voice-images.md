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
out="$(mktemp -t stt-XXXXXX)"
trap 'rm -f "$out" "$out.txt"' EXIT
"$bin" -m "$model" -f "$audio" -nt --output-txt --output-file "$out" >/dev/null
cat "$out.txt"
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
base="$(mktemp -t say-XXXXXX)"
out="${base}.ogg"
rm -f "$base" # mktemp's un-suffixed file is not the one we want; drop it
text="$(cat)"

if command -v say >/dev/null; then
  # macOS
  tmp="$(mktemp -t say-XXXXXX).aiff"
  say -o "$tmp" -- "$text"
  ffmpeg -y -loglevel error -i "$tmp" -c:a libopus "$out"
  rm -f "$tmp"
elif command -v espeak-ng >/dev/null; then
  # Linux: apt install espeak-ng ffmpeg
  tmp="$(mktemp -t say-XXXXXX).wav"
  espeak-ng -w "$tmp" -- "$text"
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
ignoring an attachment. `shell3 boot` already installs one at
`~/.shell3/skills/media.md` — it is generic (it checks whether a script is
installed before pointing at it), so it works whether you've written none,
one, or all three of the recipes above. You don't need to create it; it's
there from first boot. For reference, this is what ships:

```markdown
---
name: media
description: Use when a chat message includes an audio attachment (transcribe it before answering) or when a spoken/image reply would serve the user better than text.
---

# Voice and images

shell3 has no built-in voice or image-generation service — these are wrapper
scripts you (or the user) write once and drop into `~/.shell3/lib/bin/`, per
the `scripting` skill. This skill only applies once such scripts exist; it
does not ship any itself. See the cookbook
(`docs/cookbook/voice-images.md` in the shell3 repo) for ready-made
`stt.sh`/`say.sh`/`imagegen.sh` recipes to install.

- An inbound `audio/*` attachment: if a transcription script is installed
  (commonly `~/.shell3/lib/bin/stt.sh <path>`), run it and treat the
  transcript as what the user said before answering — the raw audio path
  alone is not useful context. Transcription here is your call to make each
  time, not something that happens automatically before the turn starts.
- An inbound image: use `read_media <path>` directly — no separate
  captioning step is needed when the main model can see images itself.
- A reply worth speaking (the user asked out loud, or asked for voice): if a
  speech script is installed, pipe the reply text into it and send the
  resulting file with `send_media_telegram` (kind `voice`, ogg/opus only —
  anything else arrives as a plain audio file, not a voice bubble).
- A reply that needs a generated image: if an image-generation script is
  installed, run it with the prompt and send the result with
  `send_media_telegram` (kind `photo`).

If no such script exists yet and the user wants one of these capabilities,
say so and offer to write it (the `scripting` skill covers the pattern —
secrets read from `.env` at point of use, never in the conversation).
```

Once you've installed the scripts above, the skill already tells the agent
to use them — nothing to wire up. If you want to change the wording,
hard-code a script path, or add project-specific instructions, edit
`~/.shell3/skills/media.md` directly (that's customisation, not creation).
Note that `shell3 boot --prompts` refreshes scaffold-shipped skills on
upgrade, including this one — it backs up your edited copy to
`.backup/prompts-<ts>/` first, but will overwrite the live file, so recheck
your customisation after running it.

## Gating

These are ordinary scripts run through `bash`/`bash_bg` — the tool-call hook
sees them like any other command (`command` carries the full invocation), so
if you want to gate image generation or paid transcription calls
separately from everything else, match on `stt.sh`/`say.sh`/`imagegen.sh` by
name in `hooks/tool-call.sh`. See
[configuration.md](../configuration.md#the-command-gate--hookssh) for the
hook's payload shape.
