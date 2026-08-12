# Captured MiniMax streams

One raw `chat.completion.chunk` JSON per line, recorded off the wire from
`api.minimax.io/v1` by `capture_probe_test.go` (build tag `probe`, needs a real
key — `go test -tags probe ./internal/adapter/openai/ -run TestCaptureStreams`).

They exist because this provider's stream shape could not be reasoned about
from the API docs: reasoning arrives duplicated into `content`, sometimes with
a sibling `reasoning`/`reasoning_content` field on the same delta and sometimes
without. `reasoning_test.go` replays these through the real split so the
partitioner is regression-tested against traffic that actually happened.

- `plain_answer` / `long_prose` / `code_fence` — ordinary replies
- `tags_in_prose` — a reply that itself discusses `<think>` tags, where the
  provider interleaves reasoning and answer beyond client-side repair (see
  TestCorpusTagsInProseIsProviderDamage)
- `tags_in_prose_split` — the same prompt with `reasoning_split: true`
