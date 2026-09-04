# Contributing

Read `AGENTS.md` and the relevant section of [docs/internals.md](docs/internals.md)
before changing a subsystem.

## Checks

Go is the only build dependency; linting additionally requires
`golangci-lint`. Before integration, run:

```sh
make build
go test ./...
make lint
```

Use `make test` for the race-enabled suite and `make acceptance` for the
deterministic config-to-workflow smoke test.

## Change discipline

- Start a `feature/<slug>` or `fix/<slug>` branch from `main`.
- Keep code, tests, and public documentation aligned.
- Preserve unrelated worktree changes.
- Prefer a rule, worked example, or extension seam over a new built-in.
- Keep the model tool surface limited to `bash`, `bash_bg`, and the optional
  Telegram file-send tool. Prefer project CLI conventions and embedded skills
  over new file-manipulation tools.
- Treat everything under `internal/` as changeable implementation detail.
- Add failure-path tests for parsing, storage, concurrency, and delivery work.
- Use `gofmt`; keep `go vet` and `golangci-lint` clean.

After verification, squash the topic to one meaningful commit on `main` and
remove the finished topic branch. Do not publish or release unless explicitly
requested.

Never commit build output, credentials, `.shell3_project/`, workflow state,
transcripts, job logs, or local agent artifacts.
