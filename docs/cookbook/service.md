# Run the bot as a service

The recommended shape: shell3 as a systemd user unit. One unit to manage,
restarted on failure, up after reboots without an active login.

One paste does all of it — unit, enable, linger:

```bash
mkdir -p ~/.config/systemd/user && cat > ~/.config/systemd/user/shell3.service <<'EOF'
[Unit]
Description=shell3
[Service]
ExecStart=%h/.local/bin/shell3 telegram
Restart=always
RestartSec=5
[Install]
WantedBy=default.target
EOF
systemctl --user enable --now shell3 && loginctl enable-linger "$USER"
```

Check on it with:

```bash
systemctl --user status shell3    # state + recent log
journalctl --user -u shell3 -f    # follow the log
```

Restart the unit after updating the binary — a running bot keeps executing
the old one. One caveat: a user service can't keep the machine awake; on a
laptop the bot is gone while the lid is closed, so disable suspend or host
shell3 on an always-on box.

Nothing is exposed by any of this: the bot only makes outbound connections,
and Telegram already reaches your devices.
