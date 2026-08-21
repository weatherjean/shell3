---
name: media
description: Use when a chat message includes an audio attachment (transcribe it before answering) or when a spoken/image reply would serve the user better than text.
---

# Voice and images

shell3 has no built-in voice or image-generation service — these are tools
you declare in `shell3.sh`, per the `building-agents` skill. This skill only
applies once such tools exist; it does not ship any itself. See the cookbook
(`docs/cookbook/voice-images.md` in the shell3 repo) for ready-made
`transcribe` / `say` / image-generation tool blocks to paste in.

- An inbound `audio/*` attachment: if a transcription tool is declared
  (commonly `transcribe {"path": "<path>"}`), call it and treat the
  transcript as what the user said before answering — the raw audio path
  alone is not useful context. Transcription here is your call to make each
  time, not something that happens automatically before the turn starts.
- An inbound image: use `read_media <path>` directly — no separate
  captioning step is needed when the main model can see images itself.
- A reply worth speaking (the user asked out loud, or asked for voice): if a
  speech tool is declared, call it with the reply text and send the file it
  returns with `send_media_telegram` (kind `voice`, ogg/opus only —
  anything else arrives as a plain audio file, not a voice bubble).
- A reply that needs a generated image: if an image-generation tool is
  declared, call it with the prompt and send the result with
  `send_media_telegram` (kind `photo`).

If no such tool exists yet and the user wants one of these capabilities,
say so and offer to declare it (the `building-agents` skill covers the
shape — secrets read from `.env` at point of use, never in the
conversation).
