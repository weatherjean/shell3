//go:build unix

package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newGenerateClients loads a config declaring a model at url and an imagegen
// block referencing it with the given size.
func newGenerateClients(t *testing.T, url, size string) *Clients {
	t.Helper()
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir()) // keep test output out of ~/.shell3/media
	return newTestClients(t, `
shell3.model("m", { base_url = "`+url+`", api_key = "k", model = "img-x" })
shell3.imagegen{ model = "m", size = "`+size+`" }
`+baseAgent, nil)
}

// newGenerateClientsOpenRouter is newGenerateClients but declares
// api = "openrouter" on the imagegen block.
func newGenerateClientsOpenRouter(t *testing.T, url, size string) *Clients {
	t.Helper()
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir()) // keep test output out of ~/.shell3/media
	return newTestClients(t, `
shell3.model("m", { base_url = "`+url+`", api_key = "k", model = "img-x" })
shell3.imagegen{ model = "m", size = "`+size+`", api = "openrouter" }
`+baseAgent, nil)
}

// testPNGBytes returns the encoded bytes of a tiny 2x2 PNG.
func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(1, 0, color.NRGBA{0, 255, 0, 255})
	img.Set(0, 1, color.NRGBA{0, 0, 255, 255})
	img.Set(1, 1, color.NRGBA{255, 255, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestGenerateWireShape(t *testing.T) {
	pngBytes := testPNGBytes(t)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/generations") {
			t.Errorf("path = %s", r.URL.Path)
		}
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.Bytes()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + b64 + `"}]}`))
	}))
	defer srv.Close()

	c := newGenerateClients(t, srv.URL, "512x512")
	if c.Generate == nil {
		t.Fatal("Generate is nil, want configured")
	}
	path, err := c.Generate(context.Background(), "a cat", "512x512")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if filepath.Ext(path) != ".png" {
		t.Errorf("path = %q, want .png ext", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, pngBytes) {
		t.Errorf("written file contents mismatch")
	}

	var req struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v (body=%s)", err, gotBody)
	}
	if req.Model != "img-x" {
		t.Errorf("model = %q, want img-x", req.Model)
	}
	if req.Prompt != "a cat" {
		t.Errorf("prompt = %q, want %q", req.Prompt, "a cat")
	}
	if req.Size != "512x512" {
		t.Errorf("size = %q, want 512x512", req.Size)
	}
	if req.ResponseFormat != "b64_json" {
		t.Errorf("response_format = %q, want b64_json", req.ResponseFormat)
	}
}

func TestGenerateEmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := newGenerateClients(t, srv.URL, "512x512")
	if _, err := c.Generate(context.Background(), "a cat", "512x512"); err == nil {
		t.Fatal("want error for empty data")
	}
}

// openRouterChatImageBody is a fake chat-completions response carrying one
// generated image as a data URL, the shape OpenRouter's image-output models
// return.
func openRouterChatImageBody(mediaType, b64 string) string {
	return `{"choices":[{"message":{"content":"here you go","images":[` +
		`{"type":"image_url","image_url":{"url":"data:` + mediaType + `;base64,` + b64 + `"}}]}}]}`
}

func TestGenerateOpenRouterWireShape(t *testing.T) {
	pngBytes := testPNGBytes(t)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.Bytes()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openRouterChatImageBody("image/png", b64)))
	}))
	defer srv.Close()

	c := newGenerateClientsOpenRouter(t, srv.URL, "512x512")
	if c.Generate == nil {
		t.Fatal("Generate is nil, want configured")
	}
	path, err := c.Generate(context.Background(), "a cat", "512x512")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if filepath.Ext(path) != ".png" {
		t.Errorf("path = %q, want .png ext", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, pngBytes) {
		t.Errorf("written file contents mismatch")
	}

	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer k")
	}

	var req struct {
		Model      string   `json:"model"`
		Modalities []string `json:"modalities"`
		Messages   []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v (body=%s)", err, gotBody)
	}
	if req.Model != "img-x" {
		t.Errorf("model = %q, want img-x", req.Model)
	}
	if len(req.Modalities) != 2 || req.Modalities[0] != "image" || req.Modalities[1] != "text" {
		t.Errorf("modalities = %v, want [image text]", req.Modalities)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "a cat" {
		t.Errorf("messages = %+v, want one user message %q", req.Messages, "a cat")
	}
	var raw map[string]any
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if _, ok := raw["size"]; ok {
		t.Errorf("size present in chat body, want omitted (route has no size param): %s", gotBody)
	}
}

func TestGenerateOpenRouterMediaTypeJPEG(t *testing.T) {
	pngBytes := testPNGBytes(t) // content doesn't need to actually be jpeg for this test
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openRouterChatImageBody("image/jpeg", b64)))
	}))
	defer srv.Close()

	c := newGenerateClientsOpenRouter(t, srv.URL, "512x512")
	path, err := c.Generate(context.Background(), "a cat", "512x512")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if filepath.Ext(path) != ".jpg" {
		t.Errorf("path = %q, want .jpg ext", path)
	}
}

func TestGenerateOpenRouterBareBase64URL(t *testing.T) {
	// Some providers return the image_url.url as bare base64 without the
	// data:...;base64, prefix; the decoder falls back to .png for those.
	pngBytes := testPNGBytes(t)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"images":[` +
			`{"type":"image_url","image_url":{"url":"` + b64 + `"}}]}}]}`))
	}))
	defer srv.Close()

	c := newGenerateClientsOpenRouter(t, srv.URL, "")
	path, err := c.Generate(context.Background(), "a cat", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if filepath.Ext(path) != ".png" {
		t.Errorf("path = %q, want .png ext", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, pngBytes) {
		t.Errorf("written file contents mismatch")
	}
}

func TestGenerateOpenRouterNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad prompt"}`))
	}))
	defer srv.Close()

	c := newGenerateClientsOpenRouter(t, srv.URL, "512x512")
	_, err := c.Generate(context.Background(), "a cat", "512x512")
	if err == nil {
		t.Fatal("want error for non-2xx response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want it to mention status 400", err)
	}
}

func TestGenerateOpenRouterNoImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"cannot draw that"}}]}`))
	}))
	defer srv.Close()

	c := newGenerateClientsOpenRouter(t, srv.URL, "512x512")
	if _, err := c.Generate(context.Background(), "a cat", "512x512"); err == nil {
		t.Fatal("want error when the reply carries no images")
	}
}

func TestGenerateNilWhenUnconfigured(t *testing.T) {
	c := newTestClients(t, `
shell3.model("m", { base_url = "http://x", model = "id" })
`+baseAgent, nil)
	if c.Generate != nil {
		t.Fatal("want nil Generate when shell3.imagegen is absent")
	}
}

func TestExtForMediaType(t *testing.T) {
	tests := []struct {
		input    string
		wantExt  string
		wantDesc string
	}{
		{"image/jpeg", ".jpg", "exact jpeg"},
		{"image/png", ".png", "exact png"},
		{"image/webp", ".webp", "exact webp"},
		{"image/jpeg; charset=utf-8", ".jpg", "jpeg with charset parameter"},
		{"Image/PNG", ".png", "uppercase png"},
		{"IMAGE/WEBP", ".webp", "uppercase webp"},
		{"image/unknown", ".png", "unknown type fallback"},
		{"", ".png", "empty type fallback"},
		{"image/jpeg;charset=utf-8", ".jpg", "jpeg without space after semicolon"},
		{"  image/png  ", ".png", "jpeg with surrounding whitespace"},
	}
	for _, tt := range tests {
		t.Run(tt.wantDesc, func(t *testing.T) {
			got := extForMediaType(tt.input)
			if got != tt.wantExt {
				t.Errorf("extForMediaType(%q) = %q, want %q", tt.input, got, tt.wantExt)
			}
		})
	}
}
