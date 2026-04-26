# patchwidgets

Three small interactive prompt widgets — **Ask**, **Pick**, **Confirm** — for inline TUI apps. Built on `patchtui`. Designed to be invoked as one-shot blocking calls or wrapped behind a CLI for use from hooks and scripts.

## What it does

- **`Ask`**: free-text single-line prompt. Default value on empty submit, optional placeholder.
- **`Pick`**: list selector with optional incremental substring filter. Returns the chosen value and original index.
- **`Confirm`**: yes/no prompt. Tab/arrows toggle, `y`/`n` submit immediately.
- **/dev/tty discipline**: every widget reads keys from and paints to `/dev/tty`, so the process's `stdin` and `stdout` stay free for piping JSON in and result JSON out.
- **Cancel + timeout**: each widget honours Esc / Ctrl+C and an optional `TimeoutSeconds`. The returned `Result.Reason` distinguishes `ok`, `cancel`, `timeout`, and `eof`.

## What it does NOT do

- No multi-line input — `Ask` is single-line; for editor-style input use `patchapp`.
- No multi-select on `Pick` — single-select only.
- No styling theme — colors are baked into the package's small palette and not configurable.
- No persistent state.

## Usage (library)

```go
import "github.com/weatherjean/shell3/internal/patchwidgets"

res, err := patchwidgets.Confirm(patchwidgets.ConfirmSpec{
    Input:   "Delete branch main?",
    Default: "no",
})
if err != nil { /* … */ }
if res.OK && res.Value.(bool) {
    // proceed
}

pick, _ := patchwidgets.Pick(patchwidgets.PickSpec{
    Input: "Pick a model",
    Choices: []patchwidgets.PickChoice{
        {Value: "claude-opus-4-7",   Label: "Opus 4.7"},
        {Value: "claude-sonnet-4-6", Label: "Sonnet 4.6"},
    },
    Filter: true,
})
```

## Usage (CLI)

The `shell3 widget ask|pick|confirm` subcommands wrap each widget. They read a JSON spec from stdin and write a JSON result to stdout. Exit code maps to the result:

| Code | Meaning                                  |
|------|------------------------------------------|
| 0    | submitted (or `confirm` returned yes)    |
| 1    | `confirm` returned no                    |
| 2    | timeout                                  |
| 130  | cancel / EOF                             |

```sh
echo '{"input":"Branch?","default":"main"}' | shell3 widget ask
# → {"ok":true,"value":"main","reason":"ok"}

echo '{"input":"Pick","choices":[{"value":"a"},{"value":"b"}]}' | shell3 widget pick
# → {"ok":true,"value":"a","index":0,"reason":"ok"}

echo '{"input":"Continue?","default":"yes"}' | shell3 widget confirm
# exit 0 with {"ok":true,"value":true,"reason":"ok"} on yes; exit 1 on no
```

## Notes

- The package depends only on `patchtui` and `golang.org/x/term`. It is intended to be liftable into a standalone module later without source changes.
- `patchtui.Size` reads the size of `os.Stdout`. When `stdout` is piped (the common CLI case), the renderer falls back to 80x24 — fine for single-screen widgets.
