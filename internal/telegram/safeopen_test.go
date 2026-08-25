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

// safeSendTree builds a config dir holding a .env, plus a workdir, and points
// the media dir at <cfg>/media the way a real install has it.
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

// A symlink with an innocent name pointing at the credentials file must be
// refused: the pre-resolution name check alone would pass it, and both the
// tool's own .env guard and the shipped hook's credential denylist judge the
// requested path text, never the target.
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

// A hardlink has a clean name AND a resolved path outside the config dir, so
// only the (dev, ino) walk of the config tree can catch it.
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

// Any other config-tree file is refused too — by path for a direct or
// symlinked reference, by inode for a hardlink.
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

// A FIFO with no writer would park the turn forever inside a plain read —
// host tools have no timeout of their own — so it must be refused outright.
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

// The media dir lives under the config dir and holds exactly what the tool
// exists to send back (generated images, saved attachments), so it stays
// sendable while the rest of the config tree does not.
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

	// A relative path resolves against the workdir.
	in, _, err = safeOpen("report.pdf", work, cfg)
	if err != nil {
		t.Fatalf("a relative path must resolve against the workdir: %v", err)
	}
	_ = in.Close()
}

// The size bound must hold at READ time: a pre-read Stat can be stale, and a
// character device reports size 0 while never reaching EOF.
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

// A cancelled turn must abandon the read rather than hold the goroutine.
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
