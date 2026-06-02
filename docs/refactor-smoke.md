# Refactor smoke checklist

Run after every phase of the patchapp extraction. If any item fails, stop
and fix before proceeding.

## Setup

```sh
make build
./shell3
```

## Interactive turn

- [ ] Welcome banner renders, status bar shows mode + provider│model.
- [ ] Type `hi`, press enter — response streams into the live preview, then
      commits to scrollback when the turn ends.
- [ ] Token counter in status bar updates after the turn.
- [ ] Markdown in response renders (bold, code fences, lists).

## Cancel + quit

- [ ] During a streaming response, press `ctrl+c` — turn cancels, scrollback
      shows `[cancelled]` (dim), input returns to idle.
- [ ] At idle, single `ctrl+c` shows the "press again to exit" hint; second
      `ctrl+c` within 500ms quits cleanly.
- [ ] On quit, the session ends without dangling DB transactions
      (check the project DB `~/.shell3/projects/<uuid>/shell3.db` is not locked;
      the `<uuid>` is in `.shell3/.ref`).

## Slash commands

- [ ] `/help` (and `/h`, `/list`, `/`) prints the full command list.
- [ ] `/clear` — context cleared, dim confirmation line.
- [ ] `/rollback` with no prior turn → `[nothing to roll back]`.
- [ ] `/rollback` after a turn → `[last turn removed from context]`,
      next message starts fresh.
- [ ] `/prune` with no arg → usage hint.
- [ ] `/prune <id>` after a tool call → stub replaces result with `pruned by user`.
- [ ] `/usage` before any turn → `[no usage data yet]`.
- [ ] `/usage` after a turn → 3-line breakdown printed.
- [ ] `/prompt` dumps system prompt + active tools.
- [ ] `/model <name>` switches provider model; status bar updates.
- [ ] `/model` (no arg) prints usage hint.
- [ ] `/truncate` toggles truncated bash output (verify with a long bash tool call).
- [ ] `/bogus` → `[unknown command: /bogus]`.

## Shell passthrough

- [ ] `!ls` releases terminal, runs ls with native output, returns to TUI
      cleanly (cursor restored, frame redrawn).
- [ ] `!vim /tmp/x` — opens vim in cooked mode, quit returns to TUI.

## Tool use (LLM-driven)

- [ ] Ask the assistant to read a file or run a command. Tool call header
      (`$ cmd` or `→ tool(args)`) renders, output renders dimmed and
      truncated (or full if `/truncate` is on).
- [ ] Interactive tool (`shell_interactive`) releases TTY then resumes.

## Resilience

- [ ] Resize the terminal mid-stream — frame redraws without garbled output.
- [ ] Long paste (multi-line) lands in the input box correctly.
- [ ] Up/down arrows navigate within multi-line input.
- [ ] Esc clears the input.

## Build/test gates

- [ ] `go build ./...` clean.
- [ ] `go test ./...` all packages pass.
