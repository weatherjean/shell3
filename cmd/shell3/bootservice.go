//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	huh "charm.land/huh/v2"

	"github.com/weatherjean/shell3/internal/paths"
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

const serviceUnitName = "shell3.service"

// serviceUnit renders the systemd user unit for `shell3 telegram`.
// Restart=always + linger (enabled separately) is what makes shell3 survive
// crashes, logouts, and reboots. PATH includes the usual user bin dirs so
// tunnel/docker helpers the agent shells out to are found.
func serviceUnit(bin, configDir, home string) string {
	return fmt.Sprintf(`[Unit]
Description=shell3 agent + Telegram bot
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

// offerSystemdService asks (TTY + systemd only) whether to run
// `shell3 telegram` as a systemd user service, and sets it up on yes: unit
// file, daemon-reload,
// enable, linger. start says whether to start it immediately as well as enable
// it. Failures are reported, never fatal: boot's config work is already done.
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
	return installSystemdService(configDir, home, start)
}

// installSystemdService writes the unit for this binary, enables it (plus
// linger), and optionally (re)starts it, verifying it actually came up.
// It never asks; `boot --service` calls it directly to repair or refresh an
// installation without redoing the whole boot.
func installSystemdService(configDir, home string, start bool) serviceState {
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
	if start && !waitServiceActive(runSystemctl, 6, func() { time.Sleep(500 * time.Millisecond) }) {
		fmt.Printf("warning: %s was started but is not running — check the log:\n", serviceUnitName)
		fmt.Printf("  journalctl --user -u %s -n 20\n", serviceUnitName)
		fmt.Println("A common cause: another process (an older shell3?) already holds the port.")
		return serviceFailed
	}
	return serviceEnabled
}

// reinstallService is `shell3 boot --service`: rewrite the unit for the
// current binary and config, enable it, restart it, and verify it is
// running — the repair path when the unit points at a stale binary or an
// old process held the port. Touches nothing else boot writes.
func reinstallService() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot --service: home dir: %w", err)
	}
	dir := paths.NewGlobal(home).Root
	cfgPath := filepath.Join(dir, "shell3.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		return fmt.Errorf("boot --service: no config at %s — run `shell3 boot` first", cfgPath)
	}
	if !systemdAvailable() {
		return fmt.Errorf("boot --service: no systemd user instance here")
	}
	if installSystemdService(dir, home, true) != serviceEnabled {
		return fmt.Errorf("boot --service: service setup failed (see warnings above)")
	}
	fmt.Printf("%s reinstalled and running — message your bot on Telegram\n", serviceUnitName)
	fmt.Printf("  journalctl --user -u %s -f   # follow the log\n", serviceUnitName)
	return nil
}

// runSystemctl is the real command runner behind waitServiceActive.
func runSystemctl(argv ...string) (string, error) {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// waitServiceActive polls `systemctl --user is-active` until the unit reports
// active, giving a crash-looping unit (port already taken, bad binary) time
// to show itself instead of telling the user the service is running.
func waitServiceActive(run func(argv ...string) (string, error), tries int, sleep func()) bool {
	for i := 0; i < tries; i++ {
		if i > 0 {
			sleep()
		}
		state, _ := run("systemctl", "--user", "is-active", serviceUnitName)
		if state == "active" {
			return true
		}
	}
	return false
}
