# run_proxy recipes

`run_proxy` is a shell command shell3 auto-starts (detached, fire-and-forget)
the first time an agent uses the model. Use it to bring up a local proxy in
front of `base_url`. Output goes to `./.shell3/proxy-<model>.log`. If a proxy is
already listening, the spawn just fails to bind and the first request decides.

## Codex subscription via npx

    run_proxy = "npx @some/codex-proxy --port 8787",
    base_url  = "http://localhost:8787/v1",

## opencode-go

    run_proxy = "opencode-go serve --port 8787",
    base_url  = "http://localhost:8787/v1",

## litellm

    run_proxy = "litellm --config ~/.shell3/litellm.yaml --port 8787",
    base_url  = "http://localhost:8787/v1",
