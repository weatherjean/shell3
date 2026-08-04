# Serve as a service, reachable from your devices

The recommended shape: shell3 as a systemd user unit, exposed over your
tailnet with `tailscale serve`. Nothing public, stable https URL, and only
one unit to manage — tailscaled is already a system service and `--bg`
serve config survives reboots. Tailscale's personal plan is free.

One paste does all of it — unit, enable, linger, tailnet:

```bash
mkdir -p ~/.config/systemd/user && cat > ~/.config/systemd/user/shell3.service <<'EOF'
[Unit]
Description=shell3
[Service]
ExecStart=%h/.local/bin/shell3 serve
Restart=always
RestartSec=5
[Install]
WantedBy=default.target
EOF
systemctl --user enable --now shell3 && loginctl enable-linger "$USER" && tailscale serve --bg 8765
```

`tailscale serve` prints the URL — `https://<machine>.<tailnet>.ts.net`
(it will ask once to enable HTTPS certs for the tailnet). Put that in
`web.url`, open it on any device signed into your tailnet, done.

## Public internet (last resort)

If a device genuinely can't join the tailnet, a cloudflared tunnel as a
second unit puts a public URL in front — anyone who finds it gets your
login page, so keep TOTP on (`shell3 boot --totp` enrols):

    # ~/.config/systemd/user/shell3-tunnel.service
    [Unit]
    Description=shell3 tunnel
    After=shell3.service
    [Service]
    ExecStart=/usr/local/bin/cloudflared tunnel --url http://127.0.0.1:8765
    Restart=always
    RestartSec=5
    [Install]
    WantedBy=default.target

A quick tunnel mints a new URL each restart; read it with
`journalctl --user -u shell3-tunnel | grep -o 'https://.*trycloudflare.com' | tail -1`.
For a stable hostname use a [named tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
(`ExecStart=cloudflared tunnel run <name>`) and set `web.url`.
