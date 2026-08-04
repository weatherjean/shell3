//go:build unix

package main

import (
	"strings"
	"testing"
)

func TestSetEnvKey(t *testing.T) {
	for _, tc := range []struct {
		name, existing, want string
	}{
		{"append to empty", "", "SHELL3_WEB_TOTP_SECRET=NEW\n"},
		{"append keeps others",
			"TELEGRAM=x\n",
			"TELEGRAM=x\nSHELL3_WEB_TOTP_SECRET=NEW\n"},
		{"replace in place",
			"A=1\nSHELL3_WEB_TOTP_SECRET=OLD\nB=2\n",
			"A=1\nSHELL3_WEB_TOTP_SECRET=NEW\nB=2\n"},
		{"append when file lacks trailing newline",
			"A=1",
			"A=1\nSHELL3_WEB_TOTP_SECRET=NEW\n"},
	} {
		got := setEnvKey(tc.existing, envWebTOTP, "NEW")
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestActivateTOTPLine(t *testing.T) {
	commented := "web:\n  workdir: /w\n  addr: 127.0.0.1:8765\n  password: env:SHELL3_WEB_PASSWORD\n  # totp_secret: env:SHELL3_WEB_TOTP_SECRET\n"
	active := "web:\n  password: env:SHELL3_WEB_PASSWORD\n  totp_secret: env:SHELL3_WEB_TOTP_SECRET\n"
	missing := "web:\n  addr: 127.0.0.1:8765\n  password: env:SHELL3_WEB_PASSWORD\n"

	got, err := activateTOTPLine(commented)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\n  totp_secret: env:SHELL3_WEB_TOTP_SECRET\n") ||
		strings.Contains(got, "#") {
		t.Errorf("uncomment: got %q", got)
	}

	got, err = activateTOTPLine(active)
	if err != nil {
		t.Fatal(err)
	}
	if got != active {
		t.Errorf("already active must be a no-op, got %q", got)
	}

	got, err = activateTOTPLine(missing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "password: env:SHELL3_WEB_PASSWORD\n  totp_secret: env:SHELL3_WEB_TOTP_SECRET\n") {
		t.Errorf("insert after password: got %q", got)
	}

	if _, err := activateTOTPLine("models:\n"); err == nil {
		t.Error("a yaml with no web password line must error, not guess")
	}
}
