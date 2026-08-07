//go:build unix

package webui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/shell3"
)

// fakeRegistrar is a hostToolRegistrar test double recording registered tools.
type fakeRegistrar struct {
	tools    []shell3.HostTool
	headless bool
}

func (f *fakeRegistrar) RegisterHostTool(t shell3.HostTool) error {
	f.tools = append(f.tools, t)
	return nil
}

func (f *fakeRegistrar) Headless() bool { return f.headless }

func TestRegisterSendFileToolSkipsHeadless(t *testing.T) {
	r := &fakeRegistrar{headless: true}
	if err := RegisterSendFileTool(r, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	if len(r.tools) != 0 {
		t.Fatalf("want no tools registered for a headless session, got %d", len(r.tools))
	}
}

func TestRegisterSendFileToolRegisters(t *testing.T) {
	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	if len(r.tools) != 1 {
		t.Fatalf("want 1 tool registered, got %d", len(r.tools))
	}
	if r.tools[0].Name != "send_file" {
		t.Errorf("Name = %q, want send_file", r.tools[0].Name)
	}
	if r.tools[0].Handler == nil {
		t.Fatal("Handler is nil")
	}
}

func TestSendFileHandlerStagesAndLinks(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	src := filepath.Join(workDir, "report.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "sent report.txt") {
		t.Errorf("out = %q, want it to mention sent report.txt", out)
	}
	if !strings.Contains(out, "/api/media/") || !strings.Contains(out, "report.txt") {
		t.Errorf("out = %q, want an /api/media/ link naming report.txt", out)
	}

	// The staged copy exists in the media dir under a unique name.
	ents, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 staged file, got %d: %v", len(ents), ents)
	}
	staged := ents[0].Name()
	if !strings.HasSuffix(staged, "report.txt") {
		t.Errorf("staged name = %q, want it to keep the original base name", staged)
	}
	data, err := os.ReadFile(filepath.Join(mediaDir, staged))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("staged content = %q, want %q", data, "hello")
	}
}

func TestSendFileHandlerImageLink(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	src := filepath.Join(workDir, "pic.png")
	if err := os.WriteFile(src, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"pic.png"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "![pic.png](/api/media/") {
		t.Errorf("out = %q, want a markdown image link for an image file", out)
	}

	// A custom name becomes the image's alt text too.
	out2, err := handler(context.Background(), `{"path":"pic.png","name":"Q3 chart"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out2, "![Q3 chart](/api/media/") {
		t.Errorf("out = %q, want the custom name as the image alt text", out2)
	}
}

func TestSendFileHandlerRefusesDotenv(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	src := filepath.Join(workDir, ".env")
	if err := os.WriteFile(src, []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":".env"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "credentials") {
		t.Errorf("out = %q, want a credentials-file refusal", out)
	}

	// A dotenv sibling is refused too.
	src2 := filepath.Join(workDir, ".env.local")
	if err := os.WriteFile(src2, []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := handler(context.Background(), `{"path":".env.local"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out2, "error:") || !strings.Contains(out2, "credentials") {
		t.Errorf("out = %q, want a credentials-file refusal", out2)
	}
}

func TestSendFileHandlerRefusesSymlinkedDotenv(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	configDir := t.TempDir()
	envPath := filepath.Join(configDir, ".env")
	if err := os.WriteFile(envPath, []byte("SECRET_MARKER=do-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	link := filepath.Join(workDir, "report.txt")
	if err := os.Symlink(envPath, link); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "credentials") {
		t.Errorf("out = %q, want a credentials-file refusal for a symlink pointing at .env", out)
	}

	ents, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("want nothing staged into the media dir, got %v", ents)
	}
}

func TestSendFileHandlerRefusesSymlinkedDotenvSibling(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	configDir := t.TempDir()
	envPath := filepath.Join(configDir, ".env.local")
	if err := os.WriteFile(envPath, []byte("SECRET_MARKER=do-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	link := filepath.Join(workDir, "notes.txt")
	if err := os.Symlink(envPath, link); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "credentials") {
		t.Errorf("out = %q, want a credentials-file refusal for a symlink pointing at .env.local", out)
	}

	ents, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("want nothing staged into the media dir, got %v", ents)
	}
}

func TestSendFileHandlerRefusesDirectory(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	sub := filepath.Join(workDir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"subdir"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "directory") {
		t.Errorf("out = %q, want a directory refusal", out)
	}
}

func TestSendFileHandlerRefusesMissingFile(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"nope.txt"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("out = %q, want an error for a missing file", out)
	}
}

func TestSendFileHandlerRefusesOversize(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	src := filepath.Join(workDir, "big.bin")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := maxSendFileBytes
	maxSendFileBytes = 4 // smaller than the 5-byte file above
	defer func() { maxSendFileBytes = orig }()

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"big.bin"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "large") {
		t.Errorf("out = %q, want an oversize refusal", out)
	}
}

func TestSendFileHandlerMissingPath(t *testing.T) {
	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if out != "error: path is required" {
		t.Errorf("out = %q, want error: path is required", out)
	}
}

func TestSendFileHandlerCustomName(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	src := filepath.Join(workDir, "report.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"report.txt","name":"Q3 report"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "[Q3 report](/api/media/") {
		t.Errorf("out = %q, want a link labelled with the custom name", out)
	}
}

func TestSendFileHandlerRefusesHardlinkedDotenv(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	secret := filepath.Join(cfgDir, ".env")
	if err := os.WriteFile(secret, []byte("KEY=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	link := filepath.Join(work, "report.txt")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hardlink not supported: %v", err)
	}
	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error: refusing to send a credentials file") {
		t.Fatalf("hardlinked .env not refused: %q", out)
	}
}

func TestSendFileHandlerRefusesConfigDirFile(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	target := filepath.Join(cfgDir, "shell3.yaml")
	if err := os.WriteFile(target, []byte("web: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newSendFileHandler(t.TempDir(), cfgDir)
	out, err := h(context.Background(), `{"path":"`+target+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error: refusing to send a file from the config directory") {
		t.Fatalf("config-dir file not refused: %q", out)
	}
}

func TestSendFileHandlerRefusesSymlinkIntoConfigDir(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	target := filepath.Join(cfgDir, "hooks.sh")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	link := filepath.Join(work, "notes.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error: refusing to send a file from the config directory") {
		t.Fatalf("symlink into config dir not refused: %q", out)
	}
}

// --- Fix round 1 (adversarial review, task-7-reviewer-a.md) ---

func TestSendFileHandlerRefusesHardlinkedNestedConfigFile(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	nested := filepath.Join(cfgDir, "projects", "x")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(nested, ".env")
	if err := os.WriteFile(secret, []byte("FAKE_KEY=sk-FAKE-NESTED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	link := filepath.Join(work, "report.txt")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hardlink not supported: %v", err)
	}
	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error: refusing to send a credentials file") {
		t.Fatalf("hardlinked nested .env not refused: %q", out)
	}
}

func TestSendFileHandlerRefusesHardlinkedConfigFile(t *testing.T) {
	cases := []string{"shell3.yaml", filepath.Join("hooks", "tool-call.sh")}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
			cfgDir := t.TempDir()
			target := filepath.Join(cfgDir, rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("stub\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			work := t.TempDir()
			link := filepath.Join(work, "notes.txt")
			if err := os.Link(target, link); err != nil {
				t.Skipf("hardlink not supported: %v", err)
			}
			h := newSendFileHandler(work, cfgDir)
			out, err := h(context.Background(), `{"path":"notes.txt"}`)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "error: refusing to send a file from the config directory") {
				t.Fatalf("hardlinked %s not refused: %q", rel, out)
			}
		})
	}
}

// The media dir normally lives inside the config dir (<configDir>/media);
// staging a generated image or an earlier send from there is send_file's
// legitimate main use case and must not be caught by the config-dir
// containment checks.
func TestSendFileHandlerAllowsFileFromMediaDirInsideConfigDir(t *testing.T) {
	cfgDir := t.TempDir()
	mediaDir := filepath.Join(cfgDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	staged := filepath.Join(mediaDir, "img-earlier.png")
	if err := os.WriteFile(staged, []byte("fake-png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newSendFileHandler(mediaDir, cfgDir)
	out, err := h(context.Background(), `{"path":"img-earlier.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("legitimate media-dir file refused: %q", out)
	}
	if !strings.Contains(out, "/api/media/") {
		t.Fatalf("out = %q, want an /api/media/ link", out)
	}
}

func TestSendFileHandlerRefusesFIFOWithoutBlocking(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	work := t.TempDir()
	fifoPath := filepath.Join(work, "p.txt")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	h := newSendFileHandler(work, t.TempDir())

	done := make(chan string, 1)
	go func() {
		out, _ := h(context.Background(), `{"path":"p.txt"}`)
		done <- out
	}()

	select {
	case out := <-done:
		if !strings.HasPrefix(out, "error:") {
			t.Fatalf("out = %q, want a refusal for a FIFO", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler blocked on a FIFO with no writer")
	}
}

func TestSendFileHandlerRefusesCharacterDevice(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	h := newSendFileHandler("", t.TempDir())
	out, err := h(context.Background(), `{"path":"/dev/null"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("out = %q, want a refusal for a character device", out)
	}
}

func TestSendFileHandlerRefusesRelativePathWithoutWorkDir(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	h := newSendFileHandler("", t.TempDir())
	out, err := h(context.Background(), `{"path":"go.mod"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "working directory") {
		t.Fatalf("out = %q, want a refusal for a relative path with no working directory", out)
	}
}

func TestSendFileHandlerSanitizesName(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	work := t.TempDir()
	src := filepath.Join(work, "ok.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newSendFileHandler(work, t.TempDir())
	out, err := h(context.Background(), `{"path":"ok.txt","name":"x](https://evil.tld/a)[y"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "](https://evil.tld") {
		t.Fatalf("out = %q, want the injected markdown link stripped from name", out)
	}
}

func TestCopyLimitedEnforcesSizeCap(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(work, "big.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 10), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	var out bytes.Buffer
	if err := copyLimited(context.Background(), &out, in, 4); err == nil {
		t.Fatal("want an error when the source exceeds the limit")
	}
	if out.Len() > 10 {
		t.Fatalf("wrote %d bytes past a 4-byte limit", out.Len())
	}
}

func TestStageMediaFileRemovesPartialFileOnOverflow(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	work := t.TempDir()
	src := filepath.Join(work, "big.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 10), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	if _, err := stageMediaFile(context.Background(), in, "big.bin", 4); err == nil {
		t.Fatal("want an error when the source exceeds the limit")
	}

	ents, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("want no partial file left in the media dir, got %v", ents)
	}
}

// --- Fix round 2 (re-review of the round-1 fixes) ---

// A hardlink to a file under <configDir>/.shell3_project must be refused —
// the inode walk skipped that subtree, disagreeing with the path-based
// check (which does refuse a direct path there), for no gain: the dir is
// small runtime data (session-token hashes, the sqlite store), not a
// legitimate send target like the media dir.
func TestSendFileHandlerRefusesHardlinkedProjectDirFile(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	projectDir := filepath.Join(cfgDir, ".shell3_project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(projectDir, "web_sessions.json")
	if err := os.WriteFile(target, []byte(`{"tokens":"FAKE"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	link := filepath.Join(work, "report.txt")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hardlink not supported: %v", err)
	}
	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error: refusing to send a file from the config directory") {
		t.Fatalf("hardlinked .shell3_project file not refused: %q", out)
	}
}

// --- Fix round 3 (final whole-branch review, task-10) ---

// When configDir is set but cannot be resolved (a transient EvalSymlinks
// failure, or any other stat error), both the path-containment check and
// the inode walk are skipped today — only the dotenv-name check survives.
// That's a fail-open: an ordinary file should still be refused rather than
// silently allowed through with reduced defenses. An empty configDir (no
// config dir configured at all) is a different, legitimate case and must
// stay unaffected — see TestSendFileHandlerAllowsOrdinaryFileWithNoConfigDir.
func TestSendFileHandlerFailsClosedWhenConfigDirUnresolvable(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// configDir is non-empty but does not exist, so EvalSymlinks(configDir)
	// fails deterministically — standing in for a transient resolution
	// failure without needing to race a real mount blip.
	cfgDir := filepath.Join(t.TempDir(), "does-not-exist")

	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("out = %q, want a refusal when the config dir cannot be resolved", out)
	}
}

// An empty configDir (the front-end genuinely has none configured) must
// stay a distinct case from "configDir set but unresolvable" — an ordinary
// file still sends.
func TestSendFileHandlerAllowsOrdinaryFileWithNoConfigDir(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newSendFileHandler(work, "")
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("out = %q, want an ordinary send to succeed with no config dir configured", out)
	}
}

// If $SHELL3_MEDIA_DIR is pointed at (or above) the config dir itself — a
// misconfiguration, but one the operator can make — the media-dir exemption
// must not swallow the whole config-dir containment check. Without a guard,
// pathWithin(resolved, mediaResolved) is true for anything under the config
// dir, and the inode walk's skip-root equals its own walk root, silently
// defeating both defenses at once.
func TestSendFileHandlerRefusesWhenMediaDirCoversConfigDir(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", cfgDir)

	secret := filepath.Join(cfgDir, ".env")
	if err := os.WriteFile(secret, []byte("KEY=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	link := filepath.Join(work, "report.txt")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hardlink not supported: %v", err)
	}

	h := newSendFileHandler(work, cfgDir)
	out, err := h(context.Background(), `{"path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("hardlinked .env sent via a media-dir-covers-config-dir misconfig: %q", out)
	}
}

// base is model-controlled (the model has bash and picks the filename) just
// like the optional display name, but unlike name it was not being run
// through sanitizeLinkName — a filename containing markdown link syntax
// could fabricate a link to an attacker-controlled host in the rendered
// chat message.
func TestSendFileHandlerSanitizesDisplayedBaseName(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	work := t.TempDir()
	// filepath.Join cleans a literal "//" in the joined relative path, so the
	// injected payload avoids a doubled slash while still exercising the
	// markdown-breakout characters sanitizeLinkName strips.
	evil := "x) [click](evil.example:1234.txt"
	if err := os.WriteFile(filepath.Join(work, evil), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newSendFileHandler(work, t.TempDir())
	out, err := h(context.Background(), `{"path":`+strconv.Quote(evil)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("legitimate send refused: %q", out)
	}
	if strings.Contains(out, "[click](evil.example") {
		t.Fatalf("out = %q, base name was not sanitized before display", out)
	}
}

// sanitizeLinkName must cap on rune boundaries, not bytes — a byte-cap can
// split a multi-byte rune and emit invalid UTF-8 into the returned markdown.
func TestSanitizeLinkNameIsRuneSafe(t *testing.T) {
	// 250 é (2 bytes each): well past the 200-rune cap either way, but a
	// byte-cap at 200 would land mid-rune.
	name := strings.Repeat("é", 250)
	got := sanitizeLinkName(name)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeLinkName produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 200 {
		t.Fatalf("sanitizeLinkName rune count = %d, want 200", n)
	}
}

// A file that vanishes from the config tree mid-walk (agent temp file,
// editor swap file) must not fail an unrelated send: fs.ErrNotExist during
// the walk is not evidence the target file is anywhere in the tree.
//
// entryVanished is the exact classifier the walk uses to decide "skip this
// entry" vs. "fail closed" — test it directly against a real failed Lstat
// (deterministic) rather than trying to win the ReadDir/Info race.
func TestEntryVanishedRecognizesNotExist(t *testing.T) {
	_, err := os.Lstat(filepath.Join(t.TempDir(), "nope.txt"))
	if err == nil {
		t.Fatal("expected an error stat-ing a nonexistent path")
	}
	if !entryVanished(err) {
		t.Fatalf("entryVanished(%v) = false, want true for a not-exist error", err)
	}
}

func TestEntryVanishedRejectsOtherErrors(t *testing.T) {
	if entryVanished(errors.New("permission denied")) {
		t.Fatal("entryVanished should not treat an arbitrary error as not-exist")
	}
}

// End-to-end regression: hammer a send with a file in the config tree being
// created and removed concurrently, so the walk is likely to hit the
// ReadDir/Info race at least once across the run. The only assertion that
// matters is that a vanished, unrelated entry never surfaces as "cannot
// verify file safety" — the specific failure mode the fix closes.
func TestSendFileHandlerToleratesConfigTreeEntryVanishingDuringWalk(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())
	cfgDir := t.TempDir()
	flaky := filepath.Join(cfgDir, "flaky.tmp")

	work := t.TempDir()
	src := filepath.Join(work, "report.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.WriteFile(flaky, []byte("x"), 0o644)
			os.Remove(flaky)
		}
	}()

	h := newSendFileHandler(work, cfgDir)
	for i := 0; i < 200; i++ {
		out, err := h(context.Background(), `{"path":"report.txt"}`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(out, "error: cannot verify file safety") {
			close(stop)
			<-done
			t.Fatalf("send failed because a vanished config-tree entry was treated as fatal: %q", out)
		}
	}
	close(stop)
	<-done
}
