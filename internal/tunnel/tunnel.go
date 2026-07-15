//go:build unix

// Package tunnel spawns a user-configured tunnel command in front of the
// dashboard (shell3.telegram dashboard.tunnel) and scrapes its output for the
// public https URL, so the host can wire the Telegram Mini App menu button
// automatically. Spawn style matches internal/modelproxy: detached process
// group, fire-and-forget, never fatal — the dashboard just stays local if the
// tunnel never yields a URL. Failure detail lands in the tunnel log file.
package tunnel

import (
	"bufio"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
)

const (
	logMaxBytes    = 10 * 1024 * 1024 // 10 MB
	logMaxArchives = 1
	// urlScanTimeout bounds how long we wait for the tunnel to print its URL.
	urlScanTimeout = 30 * time.Second
)

// bareURLRe matches an https URL with NO path — tunnel banners print the bare
// endpoint (https://xyz.trycloudflare.com), while surrounding log noise links
// docs pages that always carry a path. The URL must be followed by whitespace,
// a delimiter, or end-of-line (the scanner appends a space so EOL matches).
var bareURLRe = regexp.MustCompile(`https://[A-Za-z0-9][A-Za-z0-9.-]*(?::\d+)?/?[\s|"')\]>]`)

// Start spawns command (with every "{addr}" replaced by addr) detached, tees
// its combined output to logPath, and returns a channel that delivers the
// first bare https URL found in the output. The channel is closed without a
// value if the process's output ends or urlScanTimeout passes without one.
// Errors are never returned — a broken tunnel must not stop the bot.
func Start(command, addr, logPath string) <-chan string {
	ch := make(chan string, 1)
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(command, "{addr}", addr))
	// Detach into its own process group so Ctrl+C / shell3 exit don't kill it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err1 := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	if err1 != nil || err2 != nil {
		close(ch)
		return ch
	}
	logFile, lerr := applog.OpenFile(logPath, logMaxBytes, logMaxArchives)
	if lerr != nil {
		logFile = nil // scan without capture rather than dropping the tunnel
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_, _ = logFile.WriteString("tunnel: spawn failed: " + err.Error() + "\n")
			_ = logFile.Close()
		}
		close(ch)
		return ch
	}

	var once sync.Once
	deliver := func(u string) { once.Do(func() { ch <- u; close(ch) }) }
	giveUp := func() { once.Do(func() { close(ch) }) }

	var logMu sync.Mutex
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			if logFile != nil {
				logMu.Lock()
				_, _ = logFile.WriteString(line + "\n")
				logMu.Unlock()
			}
			// Append a space so a URL at end-of-line still hits the delimiter
			// class, then trim the captured delimiter off the match.
			if m := bareURLRe.FindString(line + " "); m != "" {
				deliver(strings.TrimRight(strings.TrimSpace(m), `|"')]>/`))
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scan(stdout) }()
	go func() { defer wg.Done(); scan(stderr) }()
	go func() {
		wg.Wait()
		// Reap so we don't leave a zombie while shell3 runs; the detached
		// tunnel keeps running if shell3 exits first.
		_ = cmd.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
		giveUp()
	}()
	go func() {
		time.Sleep(urlScanTimeout)
		giveUp()
	}()
	return ch
}
