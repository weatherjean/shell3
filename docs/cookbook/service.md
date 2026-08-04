# Serve + tunnel as services

Two systemd user units: shell3, and a cloudflared quick tunnel in front of
it. ([deploying.md](../deploying.md) has the shell3 unit alone.)

    # ~/.config/systemd/user/shell3.service
    [Unit]
    Description=shell3
    [Service]
    ExecStart=%h/.local/bin/shell3 serve
    Restart=on-failure
    [Install]
    WantedBy=default.target

    # ~/.config/systemd/user/shell3-tunnel.service
    [Unit]
    Description=shell3 tunnel
    After=shell3.service
    [Service]
    ExecStart=/usr/local/bin/cloudflared tunnel --url http://127.0.0.1:8765
    Restart=on-failure
    [Install]
    WantedBy=default.target

    systemctl --user enable --now shell3 shell3-tunnel
    loginctl enable-linger $USER

A quick tunnel mints a new URL on every restart; read it from the journal:

    journalctl --user -u shell3-tunnel | grep -o 'https://.*trycloudflare.com' | tail -1

For a stable address, use a [named tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
(same unit, `ExecStart=cloudflared tunnel run <name>`) and set `web.url` to
its hostname. Remember what a tunnel is: a shell on the public internet
behind one login — Cloudflare Access in front of it is worth having.
