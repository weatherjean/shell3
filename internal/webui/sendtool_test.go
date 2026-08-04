//go:build unix

package webui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err := RegisterSendFileTool(r, t.TempDir()); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	if len(r.tools) != 0 {
		t.Fatalf("want no tools registered for a headless session, got %d", len(r.tools))
	}
}

func TestRegisterSendFileToolRegisters(t *testing.T) {
	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, t.TempDir()); err != nil {
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
		t.Fatalf("RegisterSendFileTool: %v", err)
	}
	handler := r.tools[0].Handler

	out, err := handler(context.Background(), `{"path":"pic.png"}`)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !strings.Contains(out, "![](/api/media/") {
		t.Errorf("out = %q, want a markdown image link for an image file", out)
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
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

func TestSendFileHandlerRefusesDirectory(t *testing.T) {
	mediaDir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", mediaDir)

	workDir := t.TempDir()
	sub := filepath.Join(workDir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	r := &fakeRegistrar{}
	if err := RegisterSendFileTool(r, workDir); err != nil {
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
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
	if err := RegisterSendFileTool(r, t.TempDir()); err != nil {
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
	if err := RegisterSendFileTool(r, workDir); err != nil {
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
