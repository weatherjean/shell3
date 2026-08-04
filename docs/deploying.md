# Deploying

`shell3 serve` binds `web.addr` (default `127.0.0.1:8765`) and refuses to
start without `web.password`. Everything past that — keeping it running,
and reaching it from elsewhere — is yours. Ask an agent to walk you
through any of these; each is a few lines.

## Keep it running

systemd (Linux):

    # ~/.config/systemd/user/shell3.service
    [Unit]
    Description=shell3
    [Service]
    ExecStart=%h/.local/bin/shell3 serve
    Restart=on-failure
    [Install]
    WantedBy=default.target

    systemctl --user enable --now shell3
    loginctl enable-linger $USER   # keep it up after logout

`ExecStart` is where `install.sh` puts the binary (`~/.local/bin`, or
`$PREFIX`); add `--config <dir>` to pin a config directory other than
`~/.shell3`. Restart the unit after an update — a running server keeps
executing the old binary.

launchd (macOS), runit, or a tmux session all work the same way: run
`shell3 serve`, restart it when it dies. On a laptop, note that nothing
here prevents the machine from sleeping.

## Reach it from elsewhere

The login is the only thing between whoever finds the URL and a shell,
so prefer exposure that authenticates in its own right:

- Tailscale: `tailscale serve 8765` — reachable from your devices only.
- Cloudflare quick tunnel: `cloudflared tunnel --url http://127.0.0.1:8765`
  (prints a public https URL; anyone who finds it gets a login page).
- SSH: `ssh -L 8765:127.0.0.1:8765 host` from the machine you're on.

To run shell3 and a tunnel together as services, see
[cookbook/service.md](cookbook/service.md).

If the address is stable, set `web.url` so shell3 knows its public name.
Plain http past localhost sends the password in clear — always https.
Web push also needs https (or localhost), so it follows the same rule.
