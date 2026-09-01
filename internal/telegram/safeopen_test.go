//go:build unix

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func safeSendTree(t *testing.T) (cfg, work string) {
	t.Helper()
	cfg = t.TempDir()
	work = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, ".env"), []byte("OPENAI_API_KEY=sk-SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL3_MEDIA_DIR", filepath.Join(cfg, "media"))
	return cfg, work
}

func TestSafeOpenRefusesSymlinkToDotenv(t *testing.T) {
	cfg, work := safeSendTree(t)
	link := filepath.Join(work, "report.txt")
	if err := os.Symlink(filepath.Join(cfg, ".env"), link); err != nil {
		t.Fatal(err)
	}
	_, _, err := safeOpen(link, work, cfg)
	if err == nil {
		t.Fatal("a symlink to .env was accepted — credential exfiltration")
	}
	if !strings.Contains(err.Error(), "credentials file") {
		t.Errorf("err = %v, want a credentials-file refusal", err)
	}
}

func TestSafeOpenRefusesHardlinkToDotenv(t *testing.T) {
	cfg, work := safeSendTree(t)
	hard := filepath.Join(work, "notes.md")
	if err := os.Link(filepath.Join(cfg, ".env"), hard); err != nil {
		t.Skipf("hardlink across temp dirs unavailable: %v", err)
	}
	_, _, err := safeOpen(hard, work, cfg)
	if err == nil {
		t.Fatal("a hardlink to .env was accepted — credential exfiltration")
	}
	if !strings.Contains(err.Error(), "credentials file") {
		t.Errorf("err = %v, want a credentials-file refusal", err)
	}
}

func TestSafeOpenRefusesConfigTreeFiles(t *testing.T) {
	cfg, work := safeSendTree(t)
	kitPath := filepath.Join(cfg, "notes.md")
	if err := os.WriteFile(kitPath, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safeOpen(kitPath, work, cfg); err == nil {
		t.Error("a direct config-dir path was accepted")
	}
	hard := filepath.Join(work, "innocent.md")
	if err := os.Link(kitPath, hard); err != nil {
		t.Skipf("hardlink across temp dirs unavailable: %v", err)
	}
	if _, _, err := safeOpen(hard, work, cfg); err == nil {
		t.Error("a hardlink to a config-dir file was accepted")
	}
}

func TestSafeOpenRefusesFIFO(t *testing.T) {
	cfg, work := safeSendTree(t)
	fifo := filepath.Join(work, "pipe.txt")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := safeOpen(fifo, work, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was accepted")
		}
		if !strings.Contains(err.Error(), "non-regular") {
			t.Errorf("err = %v, want a non-regular-file refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("safeOpen blocked on a FIFO with no writer — the turn would never end")
	}
}

func TestSafeOpenAllowsMediaDirAndOrdinaryFiles(t *testing.T) {
	cfg, work := safeSendTree(t)
	img := filepath.Join(cfg, "media", "img-1.png")
	if err := os.WriteFile(img, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, _, err := safeOpen(img, work, cfg)
	if err != nil {
		t.Fatalf("a generated image in the media dir must be sendable: %v", err)
	}
	_ = in.Close()

	plain := filepath.Join(work, "report.pdf")
	if err := os.WriteFile(plain, []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, _, err = safeOpen(plain, work, cfg)
	if err != nil {
		t.Fatalf("an ordinary workdir file must be sendable: %v", err)
	}
	_ = in.Close()

	in, _, err = safeOpen("report.pdf", work, cfg)
	if err != nil {
		t.Fatalf("a relative path must resolve against the workdir: %v", err)
	}
	_ = in.Close()
}

func TestReadLimitedBoundsBeyondStat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(p, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := readLimited(context.Background(), f, 1024); err == nil {
		t.Fatal("readLimited exceeded its limit without erroring")
	}
}

func TestReadLimitedHonoursContext(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readLimited(ctx, f, 1<<20); err == nil {
		t.Fatal("readLimited ignored a cancelled context")
	}
}
