//go:build unix

package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

// newTestClients writes yamlText as shell3.yaml (plus a minimal agent.md and
// an empty .env so config.Load accepts the tree), loads it, and returns
// New(cfg, ensureProxy). ensureProxy defaults to a no-op when nil.
func newTestClients(t *testing.T, yamlText string, ensureProxy func(name, command string)) *Clients {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.yaml"), []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("---\nmodel: m\n---\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	t.Cleanup(cfg.Close)
	if ensureProxy == nil {
		ensureProxy = func(name, command string) {}
	}
	return New(cfg, ensureProxy)
}

// modelOnlyYAML declares just the model "m" — for tests asserting a media
// client is nil when its block is absent.
func modelOnlyYAML(url string) string {
	return "models:\n  m: { base_url: \"" + url + "\", model: id }\n"
}

// newTranscribeClients loads a config declaring a model at url and an stt
// block referencing it.
func newTranscribeClients(t *testing.T, url string) *Clients {
	t.Helper()
	return newTestClients(t, `
models:
  m: { base_url: "`+url+`", api_key: k, model: whisper-x }
media:
  stt: { model: m, language: en }
`, nil)
}

func TestTranscribeWireShape(t *testing.T) {
	var gotModel, gotLang, gotFile string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseMultipartForm(1 << 20)
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("no file part: %v", err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		gotFile = hdr.Filename + ":" + string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello world"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "voice.ogg")
	_ = os.WriteFile(p, []byte("OggS-bytes"), 0o644)

	c := newTranscribeClients(t, srv.URL)
	if c.Transcribe == nil {
		t.Fatal("Transcribe is nil, want configured")
	}
	got, err := c.Transcribe(context.Background(), p)
	if err != nil || got != "hello world" {
		t.Fatalf("Transcribe = %q, %v", got, err)
	}
	if gotModel != "whisper-x" || gotLang != "en" || gotAuth != "Bearer k" {
		t.Errorf("model=%q lang=%q auth=%q", gotModel, gotLang, gotAuth)
	}
	if gotFile != "voice.ogg:OggS-bytes" {
		t.Errorf("file = %q", gotFile)
	}
}

func TestTranscribeNilWhenUnconfigured(t *testing.T) {
	c := newTestClients(t, modelOnlyYAML("http://x"), nil)
	if c.Transcribe != nil {
		t.Fatal("want nil Transcribe when shell3.stt is absent")
	}
}

func TestTranscribeTooLarge(t *testing.T) {
	_, err := validateAudioPath("voice.ogg", maxAudioBytes+1)
	if err == nil {
		t.Fatal("want error for oversized audio")
	}
	if !strings.Contains(err.Error(), "25 MB") {
		t.Errorf("error should mention 25 MB, got: %v", err)
	}
}

func TestTranscribeProxyEnsured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hi"}`))
	}))
	defer srv.Close()

	var gotName, gotCmd string
	c := newTestClients(t, `
models:
  m: { base_url: "`+srv.URL+`", model: whisper-x, run_proxy: run-me }
media:
  stt: { model: m }
`, func(name, command string) {
		gotName, gotCmd = name, command
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "voice.ogg")
	_ = os.WriteFile(p, []byte("x"), 0o644)

	if gotName != "" {
		t.Fatalf("ensureProxy called before first use: name=%q", gotName)
	}
	if _, err := c.Transcribe(context.Background(), p); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotName != "m" || gotCmd != "run-me" {
		t.Errorf("ensureProxy(name, command) = (%q, %q), want (m, run-me)", gotName, gotCmd)
	}
}
