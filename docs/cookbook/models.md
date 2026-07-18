# model recipes — provider-specific parameters

Most models need only `base_url`, `api_key`, and `model`. Some need an extra,
non-standard request parameter that shell3 doesn't model as a first-class
option. The `extra` table is the escape hatch: its keys are injected **verbatim
into the top-level request JSON**, so any vendor-specific knob works.

    extra:
      some_param: value    # becomes "some_param": "value" in the request body

## MiniMax M3 — `reasoning_split`

MiniMax-M3 emits its chain-of-thought inline as `<think>…</think>` inside the
message content by default. `reasoning_split: true` tells it to route the
thinking into the standard `reasoning_content` field instead, so shell3 renders
it as dim reasoning rather than leaking `<think>` tags into the answer:

    models:
      minimax:
        base_url: https://api.minimax.io/v1
        api_key: env:MINIMAX_API_KEY     # in .env beside shell3.yaml
        model: MiniMax-M3
        context_window: 128000
        compact_at: 100000
        extra:
          reasoning_split: true  # route thinking to reasoning_content, not inline <think>

## Other examples

    extra: { verbosity: high }                    # gpt-5-style verbosity control
    extra: { provider: { order: [anthropic] } }   # OpenRouter provider routing (nesting works)
