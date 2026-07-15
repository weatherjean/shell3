//go:build unix

package tunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStart_ScansFirstBareURL(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "tunnel.log")
	// Doc-link URLs (with a path) must be skipped; the bare host URL wins.
	cmd := `echo "see https://developers.cloudflare.com/tunnel/docs for help"; ` +
		`echo "|  https://abc-def.trycloudflare.com  |"; sleep 0.2`
	ch := Start(cmd, "127.0.0.1:8765", logPath)
	select {
	case url, ok := <-ch:
		if !ok || url != "https://abc-def.trycloudflare.com" {
			t.Fatalf("got %q (ok=%v)", url, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no URL scanned")
	}
	// The scanner keeps teeing after delivery; give the tail a moment.
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "trycloudflare.com") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("output not teed to log: %q", string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStart_SubstitutesAddr(t *testing.T) {
	// {addr} must be substituted BEFORE exec; the URL containing the substituted
	// addr is host-only and scans cleanly.
	ch := Start(`echo "https://{addr}"`, "ok.example.com", filepath.Join(t.TempDir(), "t.log"))
	select {
	case url, ok := <-ch:
		if !ok || url != "https://ok.example.com" {
			t.Fatalf("got %q (ok=%v)", url, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no URL scanned")
	}
}

func TestStart_NoURLClosesChannel(t *testing.T) {
	ch := Start(`echo "no urls here"`, "127.0.0.1:1", filepath.Join(t.TempDir(), "t.log"))
	select {
	case url, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel, got %q", url)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel never closed after process exit")
	}
}

func TestStart_SpawnFailureClosesChannel(t *testing.T) {
	// An unwritable log path is tolerated; a command that exits non-zero without
	// output must still close the channel.
	ch := Start(`exit 3`, "x", filepath.Join(t.TempDir(), "t.log"))
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel with no URL")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel never closed")
	}
}
