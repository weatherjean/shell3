# Voice and images

shell3 saves a chat attachment to `~/.shell3/media/` and puts the path in the
prompt. The agent perceives it with `read_media` (needs `media` in `use:`) and
sends one back with `send_media_telegram`.

Everything past that — transcribing a voice note, speaking a reply, generating
an image — is a declared tool. Paste these into your `shell3.sh` and run
`shell3 tool check ~/.shell3/shell3.sh`.

Each tool reads the one key it needs from `.env` at point of use, so no secret
enters the conversation or the agent's environment.

## Transcribe a voice note

```sh
#---
# tool: transcribe
# description: Transcribe an audio file to text. Use on a voice note before answering it.
# params:
#   path: {type: string, required: true, description: audio file path}
#---
main_transcribe() {
  local key
  key=$(grep -m1 '^OPENAI_API_KEY=' ~/.shell3/.env | cut -d= -f2-)
  [ -n "$key" ] || { echo "OPENAI_API_KEY missing from ~/.shell3/.env" >&2; return 1; }
  curl -sS --fail-with-body https://api.openai.com/v1/audio/transcriptions \
    -H "Authorization: Bearer $key" \
    -F model=whisper-1 \
    -F "file=@${path}" \
  | jq -r '.text'
}

#---
# test: transcribe — surfaces a missing key
#---
main_test_transcribe() {
  stub grep <<'STUB'
STUB
  assert_contains "$(tool transcribe path=/tmp/x.ogg 2>&1)" "OPENAI_API_KEY missing"
}
```

## Speak a reply

```sh
#---
# tool: say
# description: Render text as speech. Returns the path of the audio file to send.
# params:
#   text:  {type: string, required: true}
#   voice: {type: string, default: alloy}
#---
main_say() {
  local key out
  key=$(grep -m1 '^OPENAI_API_KEY=' ~/.shell3/.env | cut -d= -f2-)
  [ -n "$key" ] || { echo "OPENAI_API_KEY missing from ~/.shell3/.env" >&2; return 1; }
  out="$HOME/.shell3/media/say-$$.mp3"
  curl -sS --fail-with-body https://api.openai.com/v1/audio/speech \
    -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg t "$text" --arg v "$voice" \
          '{model:"gpt-4o-mini-tts", input:$t, voice:$v}')" \
    -o "$out" || return 1
  echo "$out"
}
```

The agent then calls `send_media_telegram` with that path.

## Generate an image

```sh
#---
# tool: image
# description: Generate an image from a prompt. Returns the path of the file to send.
# params:
#   prompt: {type: string, required: true}
#   size:   {type: string, default: 1024x1024}
#---
main_image() {
  local key out
  key=$(grep -m1 '^OPENAI_API_KEY=' ~/.shell3/.env | cut -d= -f2-)
  [ -n "$key" ] || { echo "OPENAI_API_KEY missing from ~/.shell3/.env" >&2; return 1; }
  out="$HOME/.shell3/media/img-$$.png"
  curl -sS --fail-with-body https://api.openai.com/v1/images/generations \
    -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg p "$prompt" --arg s "$size" \
          '{model:"gpt-image-1", prompt:$p, size:$s, n:1}')" \
  | jq -r '.data[0].b64_json' | base64 -d > "$out" || return 1
  echo "$out"
}
```

## Telling the agent when to use them

Put the policy in a skill rather than in each tool's description — a skill is a
file, so this is `~/.shell3/skills/media-policy.md`:

```markdown
---
name: media-policy
description: When to transcribe a voice note, speak a reply, or generate an image
---

A voice note arrives as a file path. Transcribe it before answering — never
guess at audio you have not read.

Reply with speech only when I sent speech. Generate an image only when I ask
for one; describe it in words first if the request is ambiguous.
```

## Housekeeping

Generated files land in `~/.shell3/media/`, which the startup janitor sweeps
when `media_keep_days` is set. A swept file's path in an old transcript stops
resolving, which is intended — the transcript is the record, not the media.
