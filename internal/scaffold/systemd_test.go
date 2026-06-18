package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTelegramSystemd(t *testing.T) {
	dir := t.TempDir()
	v := SystemdValues{
		ConfigDir:   "/root/.shell3/telegram",
		DefaultBin:  "/usr/local/bin/shell3",
		ServiceName: "shell3-telegram",
		Home:        "/root",
	}
	if err := RenderTelegramSystemd(dir, v, false); err != nil {
		t.Fatalf("RenderTelegramSystemd: %v", err)
	}

	unit, err := os.ReadFile(filepath.Join(dir, "shell3-telegram.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	for _, want := range []string{
		"ExecStart=/usr/local/bin/shell3 telegram --config /root/.shell3/telegram/shell3.lua",
		"Environment=HOME=/root",
		"WorkingDirectory=/root/.shell3/telegram",
		"Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit missing %q", want)
		}
	}
	if strings.Contains(string(unit), "{{") {
		t.Error("unit still has an unrendered template delimiter")
	}

	scriptPath := filepath.Join(dir, "install-systemd.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	for _, want := range []string{
		`SERVICE_NAME="shell3-telegram"`,
		`DEFAULT_BIN="/usr/local/bin/shell3"`,
		`SRC_CONFIG_DIR="/root/.shell3/telegram"`,
		`SRC_HOME="/root"`,
		`useradd --system`,
		"sed -i",
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script missing %q", want)
		}
	}
	if strings.Contains(string(script), "{{") {
		t.Error("script still has an unrendered template delimiter")
	}

	fi, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("install-systemd.sh perms = %v, want 0755", fi.Mode().Perm())
	}

	// The generated installer must be valid bash.
	if bash, err := exec.LookPath("bash"); err == nil {
		if out, err := exec.Command(bash, "-n", scriptPath).CombinedOutput(); err != nil {
			t.Errorf("bash -n failed: %v\n%s", err, out)
		}
	}
}

func TestRenderTelegramSystemdIdempotent(t *testing.T) {
	dir := t.TempDir()
	v := SystemdValues{ConfigDir: "/root/.shell3/telegram", DefaultBin: "/usr/local/bin/shell3", ServiceName: "shell3-telegram", Home: "/root"}
	if err := RenderTelegramSystemd(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	// Tamper, then a non-force re-render must leave the file untouched.
	scriptPath := filepath.Join(dir, "install-systemd.sh")
	if err := os.WriteFile(scriptPath, []byte("# edited\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RenderTelegramSystemd(dir, v, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(scriptPath)
	if string(got) != "# edited\n" {
		t.Error("non-force re-render overwrote an existing install-systemd.sh")
	}
}
