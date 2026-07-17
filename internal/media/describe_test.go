//go:build unix

package media

import (
	"bytes"
	"context"
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

// newDescribeClients loads a config declaring a model at url and a describe
// block referencing it with the given prompt.
func newDescribeClients(t *testing.T, url, prompt string) *Clients {
	t.Helper()
	return newTestClients(t, `
shell3.model("m", { base_url = "`+url+`", api_key = "k", model = "vision-x" })
shell3.describe{ model = "m", prompt = "`+prompt+`" }
`+baseAgent, nil)
}

// writeTestPNG encodes a tiny 2x2 image.NRGBA as a real PNG file at path, so
// LoadMediaPart's decode/resize path is genuinely exercised.
func writeTestPNG(t *testing.T, path string) {
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
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDescribeWireShape(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		var err error
		gotBody, err = func() ([]byte, error) {
			buf := new(bytes.Buffer)
			_, err := buf.ReadFrom(r.Body)
			return buf.Bytes(), err
		}()
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a red square"}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "pic.png")
	writeTestPNG(t, p)

	c := newDescribeClients(t, srv.URL, "what is in this image?")
	if c.Describe == nil {
		t.Fatal("Describe is nil, want configured")
	}
	got, err := c.Describe(context.Background(), p)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got != "a red square" {
		t.Errorf("Describe = %q", got)
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v (body=%s)", err, gotBody)
	}
	if req.Model != "vision-x" {
		t.Errorf("model = %q, want vision-x", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v", req.Messages)
	}
	content := req.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("content parts = %d, want 2: %+v", len(content), content)
	}
	var sawText, sawImage bool
	for _, part := range content {
		switch part.Type {
		case "text":
			sawText = true
			if part.Text != "what is in this image?" {
				t.Errorf("text part = %q", part.Text)
			}
		case "image_url":
			sawImage = true
			if !strings.HasPrefix(part.ImageURL.URL, "data:image/jpeg;base64,") {
				t.Errorf("image_url = %q", part.ImageURL.URL)
			}
		}
	}
	if !sawText || !sawImage {
		t.Errorf("content parts missing text or image_url: %+v", content)
	}
}

func TestDescribeBadFile(t *testing.T) {
	c := newDescribeClients(t, "http://unused", "describe this")
	if _, err := c.Describe(context.Background(), "/no/such/file.png"); err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestDescribeNilWhenUnconfigured(t *testing.T) {
	c := newTestClients(t, `
shell3.model("m", { base_url = "http://x", model = "id" })
`+baseAgent, nil)
	if c.Describe != nil {
		t.Fatal("want nil Describe when shell3.describe is absent")
	}
}
