//go:build unix

package main

// `shell3 boot --totp`: enrol (or reset) the authenticator second factor for
// an existing config. Declining the offer at boot writes a commented
// totp_secret line; this is the way back — and, because it always mints a
// fresh secret, also the lost-phone reset. Turning the factor OFF stays
// manual and documented: delete SHELL3_WEB_TOTP_SECRET from .env and restart.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/paths"
)

// runTOTPEnrol mints a fresh secret against the existing config: QR to scan,
// secret into .env (replacing any previous one), totp_secret line activated
// in shell3.yaml.
func runTOTPEnrol() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot --totp: home dir: %w", err)
	}
	dir := paths.NewGlobal(home).Root
	cfgPath := filepath.Join(dir, "shell3.yaml")
	yamlText, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("boot --totp needs an existing config (run plain `shell3 boot` first): %w", err)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("boot --totp is interactive (it shows a QR code to scan) — run it in a terminal")
	}

	// The account label is what the authenticator app displays under the
	// "shell3" issuer; the config dir keeps two machines' entries apart.
	secret, uri, err := newTOTPEnrolment(filepath.Base(dir))
	if err != nil {
		return err
	}

	// The yaml edit is computed up front so a config this can't handle fails
	// before the QR ceremony, but nothing is WRITTEN until a code from the
	// scanned entry verifies: an unconfirmed secret on disk is a lockout.
	yamlOut, err := activateTOTPLine(string(yamlText))
	if err != nil {
		return err
	}

	printTOTPEnrolment(secret, uri, os.Stdout)
	verified, err := verifyTOTPEnrolment(secret)
	if err != nil {
		return err
	}
	if !verified {
		fmt.Println("\nCancelled — nothing changed.")
		return nil
	}

	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot --totp: read .env: %w", err)
	}
	if err := atomicWriteFile(envPath, []byte(setEnvKey(string(existing), envWebTOTP, secret)), 0o600); err != nil {
		return fmt.Errorf("boot --totp: write .env: %w", err)
	}
	if yamlOut != string(yamlText) {
		if err := atomicWriteFile(cfgPath, []byte(yamlOut), 0o644); err != nil {
			return fmt.Errorf("boot --totp: write shell3.yaml: %w", err)
		}
	}

	fmt.Println("\nSecond factor is on: any previous authenticator entry is now stale.")
	fmt.Println("Restart `shell3 serve` to apply. Lost phone? Delete " + envWebTOTP + " from .env and restart.")
	return nil
}

// setEnvKey returns existing with key set to value: the KEY=… line replaced in
// place when present, appended otherwise. Never prints the value anywhere but
// the returned content.
func setEnvKey(existing, key, value string) string {
	line := key + "=" + value
	lines := strings.Split(existing, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	out := existing
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + line + "\n"
}

// activeTOTPLine is the yaml line boot writes when enrolment is taken; the
// scaffold writes it commented out when declined.
const activeTOTPLine = "totp_secret: env:SHELL3_WEB_TOTP_SECRET"

// activateTOTPLine returns yamlText with the web block's totp_secret line
// active: uncommented when the scaffold's commented hint is present, untouched
// when already active, inserted directly under the web password line
// otherwise. A yaml with no web password line is not one boot wrote — error
// rather than guess where the web block is.
func activateTOTPLine(yamlText string) (string, error) {
	lines := strings.Split(yamlText, "\n")
	passwordAt := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == activeTOTPLine {
			return yamlText, nil
		}
		if strings.TrimSpace(strings.TrimPrefix(t, "#")) == activeTOTPLine && strings.HasPrefix(t, "#") {
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = indent + activeTOTPLine
			return strings.Join(lines, "\n"), nil
		}
		if strings.HasPrefix(t, "password:") && passwordAt == -1 {
			passwordAt = i
		}
	}
	if passwordAt == -1 {
		return "", fmt.Errorf("boot --totp: no `password:` line in shell3.yaml — is this a boot-written config?")
	}
	indent := lines[passwordAt][:len(lines[passwordAt])-len(strings.TrimLeft(lines[passwordAt], " \t"))]
	lines = append(lines[:passwordAt+1],
		append([]string{indent + activeTOTPLine}, lines[passwordAt+1:]...)...)
	return strings.Join(lines, "\n"), nil
}
