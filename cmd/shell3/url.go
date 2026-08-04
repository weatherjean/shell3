//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/tunnel"
)

// newURLCommand prints where the interface is reachable, so nobody has to
// remember a journalctl incantation to find the tunnel URL.
func newURLCommand() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "url",
		Short: "Print where the web interface is reachable (tunnel URL when one runs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := configDir
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("url: home dir: %w", err)
				}
				dir = paths.NewGlobal(home).Root
			}
			u, note, err := resolvePublicURL(dir)
			if err != nil {
				return err
			}
			fmt.Println(u)
			if note != "" {
				fmt.Fprintln(os.Stderr, note)
			}
			return nil
		},
	}
	addConfigFlag(cmd, &configDir)
	return cmd
}

// webWiring scans the config for how the interface is exposed: a fixed
// web.url, whether web.tunnel is wired, and the listen address. A line scan,
// like showBootSuccess — the strict loader needs live .env keys, which a
// status query must not require.
func webWiring(dir string) (fixedURL string, tunnelWired bool, addr string, err error) {
	yaml, err := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if err != nil {
		return "", false, "", fmt.Errorf("no config at %s — run `shell3 boot` first", filepath.Join(dir, "shell3.yaml"))
	}
	addr = "127.0.0.1:8765"
	for _, line := range strings.Split(string(yaml), "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "url:"); ok {
			if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
				fixedURL = v
			}
		}
		if v, ok := strings.CutPrefix(t, "addr:"); ok {
			if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
				addr = v
			}
		}
		if strings.HasPrefix(t, "tunnel:") {
			tunnelWired = true
		}
	}
	return fixedURL, tunnelWired, addr, nil
}

// resolvePublicURL reports the interface address for the config in dir, most
// public first: a fixed web.url, then the last tunnel-scraped URL
// (tunnel.url), then the local listen address. The note (when non-empty) is
// caveat text for stderr, so scripts capturing stdout get the bare URL.
func resolvePublicURL(dir string) (url, note string, err error) {
	fixedURL, tunnelWired, addr, err := webWiring(dir)
	if err != nil {
		return "", "", fmt.Errorf("url: %w", err)
	}
	if fixedURL != "" {
		return fixedURL, "", nil
	}
	if b, ferr := os.ReadFile(filepath.Join(dir, tunnel.URLFileName)); ferr == nil {
		if u := strings.TrimSpace(string(b)); u != "" {
			return u, "(from the last tunnel start — a restarted quick tunnel mints a new URL)", nil
		}
	}
	if tunnelWired {
		return "http://" + addr, "(tunnel wired but no URL scraped yet — is the server running?)", nil
	}
	return "http://" + addr, "", nil
}

// waitTunnelURL polls for a tunnel.url written after since, returning it or
// "" when the deadline passes — a quick tunnel takes a few seconds to mint
// its hostname after the service starts.
func waitTunnelURL(dir string, since time.Time, deadline time.Duration) string {
	path := filepath.Join(dir, tunnel.URLFileName)
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if st, err := os.Stat(path); err == nil && st.ModTime().After(since) {
			if b, err := os.ReadFile(path); err == nil {
				if u := strings.TrimSpace(string(b)); u != "" {
					return u
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}
