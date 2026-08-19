# Deploying

`shell3 telegram` connects outbound to Telegram; the only listener is the
read-only dash on `127.0.0.1`, which needs no exposing (tunnel it later with
`/dash help exposing` if you want it on your phone) — deploying is only
**keeping the process running**. Ask an agent to walk you through any of
this; it's a few lines.

## Keep it running

systemd (Linux):

    # ~/.config/systemd/user/shell3.service
    [Unit]
    Description=shell3
    [Service]
    ExecStart=%h/.local/bin/shell3 telegram
    Restart=on-failure
    [Install]
    WantedBy=default.target

    systemctl --user enable --now shell3
    loginctl enable-linger $USER   # keep it up after logout

`ExecStart` is where `install.sh` puts the binary (`~/.local/bin`, or
`$PREFIX`); add `--config <dir>` to pin a config directory other than
`~/.shell3`. Restart the unit after an update — a running bot keeps
executing the old binary.

launchd (macOS), runit, or a tmux session all work the same way: run
`shell3 telegram`, restart it when it dies. On a laptop, note that nothing
here prevents the machine from sleeping — the bot is gone while the lid is
closed; disable suspend or host shell3 on an always-on box.

The full recipe is [cookbook/service.md](cookbook/service.md).

## Reaching it from elsewhere

Nothing to do: Telegram already reaches every device you're signed into.
Access control is the bot token plus the one `chat_id` the bot answers —
see [security.md](security.md#the-telegram-boundary).
