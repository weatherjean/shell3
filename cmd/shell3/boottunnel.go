//go:build unix

package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	huh "charm.land/huh/v2"
)

// offerCloudflared gently offers to install cloudflared when it's missing:
// the scaffold's dashboard tunnel uses it, but the bot runs fine without —
// so this is strictly opt-in (TTY only), never uses sudo, and every failure
// mode is a shrug plus a pointer, not an error. Declining or failing leaves
// boot's work fully intact.
func offerCloudflared(tty bool) {
	if !tty {
		return
	}
	if _, err := exec.LookPath("cloudflared"); err == nil {
		return // already installed
	}
	install := true
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Install cloudflared so the dashboard reaches your phone?").
			Description("Optional. It tunnels the Mini App dashboard (free, no account).\n" +
				"One binary into ~/.local/bin — no sudo, nothing else touched.\n" +
				"Skipping is fine: the dashboard just stays local, and you can\n" +
				"install it any time later.").
			Value(&install),
	)).Run()
	if err != nil || !install {
		fmt.Println("no problem — the dashboard stays local. cloudflared can be added any time:")
		fmt.Println("  https://github.com/cloudflare/cloudflared")
		return
	}
	if err := installCloudflared(); err != nil {
		fmt.Printf("that didn't work out (%v) — no harm done, the dashboard stays local.\n", err)
		fmt.Println("Manual install: https://github.com/cloudflare/cloudflared")
	}
}

// installCloudflared installs cloudflared the least intrusive way available:
// Homebrew when present (macOS convention), otherwise a direct download of
// the official release binary into ~/.local/bin.
func installCloudflared() error {
	if _, err := exec.LookPath("brew"); err == nil {
		fmt.Println("installing via Homebrew (can take a minute)…")
		cmd := exec.Command("brew", "install", "cloudflared")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("brew install cloudflared: %w", err)
		}
		fmt.Println("cloudflared installed.")
		return nil
	}
	return downloadCloudflared()
}

// cloudflaredAssetURL maps GOOS/GOARCH to the official release asset. Linux
// assets are bare binaries; darwin ships a .tgz.
func cloudflaredAssetURL() (url string, tgz bool, err error) {
	base := "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		switch arch {
		case "amd64", "arm64", "386", "arm":
			return base + "cloudflared-linux-" + arch, false, nil
		}
	case "darwin":
		switch arch {
		case "amd64", "arm64":
			return base + "cloudflared-darwin-" + arch + ".tgz", true, nil
		}
	}
	return "", false, fmt.Errorf("no prebuilt cloudflared for %s/%s", runtime.GOOS, arch)
}

// downloadCloudflared fetches the release asset into ~/.local/bin/cloudflared
// (staged via a temp file so a broken download never leaves a half-written
// binary) and verifies it runs.
func downloadCloudflared() error {
	url, tgz, err := cloudflaredAssetURL()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(binDir, "cloudflared")

	fmt.Printf("downloading %s …\n", url)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}

	var src io.Reader = resp.Body
	if tgz {
		if src, err = cloudflaredFromTgz(resp.Body); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(binDir, ".cloudflared-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the successful rename
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if out, err := exec.Command(tmp.Name(), "--version").CombinedOutput(); err != nil {
		return fmt.Errorf("downloaded binary does not run: %v\n%s", err, out)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return err
	}
	fmt.Printf("cloudflared installed to %s\n", dest)
	if !strings.Contains(":"+os.Getenv("PATH")+":", ":"+binDir+":") {
		fmt.Printf("note: %s is not on your PATH — add it so `cloudflared` resolves in your shell.\n", binDir)
		fmt.Println("(the systemd unit boot writes already includes it)")
	}
	return nil
}

// cloudflaredFromTgz returns a reader positioned at the cloudflared binary
// inside the darwin release tarball.
func cloudflaredFromTgz(r io.Reader) (io.Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("cloudflared binary not found in release tarball")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == "cloudflared" && hdr.Typeflag == tar.TypeReg {
			return tr, nil
		}
	}
}
