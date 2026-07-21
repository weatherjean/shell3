//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	huh "charm.land/huh/v2"
)

// serviceState describes what the boot-time systemd step did, for the final
// success message.
type serviceState int

const (
	serviceNotOffered serviceState = iota // no systemd / no TTY / not asked
	serviceDeclined
	serviceEnabled // unit written + enabled (and started when startable)
	serviceFailed  // attempted but a systemctl step failed
)

// systemdAvailable reports whether a user systemd instance is reachable:
// the runtime dir marks a systemd boot, and systemctl must be on PATH.
func systemdAvailable() bool {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

const serviceUnitName = "shell3-telegram.service"

// serviceUnit renders the systemd user unit for `shell3 telegram`.
// Restart=always + linger (enabled separately) is what makes the bot survive
// crashes, logouts, and reboots. PATH includes the usual user bin dirs so
// tunnel/docker helpers the agent shells out to are found.
func serviceUnit(bin, configDir, home string) string {
	return fmt.Sprintf(`[Unit]
Description=shell3 Telegram bot front-end
After=network-online.target
Wants=network-online.target
StartLimitBurst=5
StartLimitIntervalSec=60

[Service]
Type=simple
ExecStart=%s telegram --config %s
Restart=always
RestartSec=5
Environment=HOME=%s
Environment=PATH=/usr/local/bin:/usr/bin:/bin:%s/.local/bin:%s/bin

[Install]
WantedBy=default.target
`, bin, configDir, home, home, home)
}

// offerSystemdService asks (TTY + systemd only) whether to run `shell3
// telegram` as a systemd user service, and sets it up on yes: unit file,
// daemon-reload, enable, linger. start says whether the config is complete
// enough to start it immediately (Telegram token + chat id present) — an
// incomplete config is enabled but not started, so it can't crash-loop.
// Failures are reported, never fatal: boot's config work is already done.
func offerSystemdService(tty bool, configDir, home string, start bool) serviceState {
	if !tty || !systemdAvailable() {
		return serviceNotOffered
	}
	install := true
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Run shell3 as a systemd user service?").
			Description("Auto-starts on boot and restarts on crash (unit: " + serviceUnitName + ").").
			Value(&install),
	)).Run()
	if err != nil || !install {
		return serviceDeclined
	}

	bin, err := os.Executable()
	if err != nil {
		fmt.Printf("warning: service setup skipped — cannot resolve the shell3 binary path: %v\n", err)
		return serviceFailed
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		fmt.Printf("warning: service setup failed: %v\n", err)
		return serviceFailed
	}
	unitPath := filepath.Join(unitDir, serviceUnitName)
	if err := atomicWriteFile(unitPath, []byte(serviceUnit(bin, configDir, home)), 0o644); err != nil {
		fmt.Printf("warning: service setup failed writing %s: %v\n", unitPath, err)
		return serviceFailed
	}

	steps := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", serviceUnitName},
		// Linger keeps the user manager (and the bot) running with no login
		// session — without it the service dies on logout and won't start at
		// boot. May prompt for auth on some distros; a failure is reported.
		{"loginctl", "enable-linger"},
	}
	if start {
		steps = append(steps, []string{"systemctl", "--user", "restart", serviceUnitName})
	}
	for _, argv := range steps {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			fmt.Printf("warning: %s failed: %v\n%s", strings.Join(argv, " "), err, out)
			return serviceFailed
		}
	}
	return serviceEnabled
}
