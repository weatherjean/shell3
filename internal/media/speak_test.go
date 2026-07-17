//go:build unix

package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// newSpeakClients loads a config declaring a model at url and a tts block
// referencing it with the given format.
func newSpeakClients(t *testing.T, url, format string) *Clients {
	t.Helper()
	return newTestClients(t, `
shell3.model("m", { base_url = "`+url+`", api_key = "k", model = "tts-x" })
shell3.tts{ model = "m", voice = "alloy", format = "`+format+`" }
`+baseAgent, nil)
}

func TestSpeakWireShape(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/audio/speech") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("AUDIO"))
	}))
	defer srv.Close()

	c := newSpeakClients(t, srv.URL, "opus")
	if c.Speak == nil {
		t.Fatal("Speak is nil, want configured")
	}
	sp, err := c.Speak(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}

	want := map[string]any{"model": "tts-x", "input": "hello", "voice": "alloy", "response_format": "opus"}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}

	wantDir := filepath.Join(os.TempDir(), "shell3-media")
	if filepath.Dir(sp.Path) != wantDir {
		t.Errorf("Path dir = %s, want %s", filepath.Dir(sp.Path), wantDir)
	}
	if filepath.Ext(sp.Path) != ".ogg" {
		t.Errorf("ext = %s, want .ogg", filepath.Ext(sp.Path))
	}
	b, err := os.ReadFile(sp.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "AUDIO" {
		t.Errorf("contents = %q, want AUDIO", string(b))
	}
	if !sp.VoiceCompatible {
		t.Error("VoiceCompatible = false, want true for opus")
	}
}

func TestSpeakMp3NotVoiceCompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("AUDIO"))
	}))
	defer srv.Close()

	c := newSpeakClients(t, srv.URL, "mp3")
	sp, err := c.Speak(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if filepath.Ext(sp.Path) != ".mp3" {
		t.Errorf("ext = %s, want .mp3", filepath.Ext(sp.Path))
	}
	if sp.VoiceCompatible {
		t.Error("VoiceCompatible = true, want false for mp3")
	}
}

func TestStripMarkdownForSpeech(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "fenced code block",
			in:   "```\nline1\nline2\n```",
			want: "line1\nline2\n",
		},
		{
			name: "inline code backticks",
			in:   "`code`",
			want: "code",
		},
		{
			name: "bold with asterisks",
			in:   "**bold**",
			want: "bold",
		},
		{
			name: "bold with underscores",
			in:   "__bold__",
			want: "bold",
		},
		{
			name: "italic with asterisks",
			in:   "*italic*",
			want: "italic",
		},
		{
			name: "italic with underscores",
			in:   "_italic_",
			want: "italic",
		},
		{
			name: "link with text and URL",
			in:   "[link text](http://example.com)",
			want: "link text",
		},
		{
			name: "heading level 1",
			in:   "# Heading",
			want: "Heading",
		},
		{
			name: "heading level 2",
			in:   "## Heading",
			want: "Heading",
		},
		{
			name: "dash bullet",
			in:   "- item",
			want: "item",
		},
		{
			name: "asterisk bullet",
			in:   "* item",
			want: "item",
		},
		{
			name: "composite with multiple transforms",
			in:   "**bold** and `code` and [link](http://x) and\n# Head\n- item",
			want: "bold and code and link and\nHead\nitem",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdownForSpeech(tt.in)
			if got != tt.want {
				t.Errorf("StripMarkdownForSpeech(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCapSpeechText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
	}{
		{
			name:   "ASCII truncation",
			input:  strings.Repeat("x", 5000),
			maxLen: 4000,
		},
		{
			name:   "multi-byte rune mid-split truncation",
			input:  strings.Repeat("éa", 3000), // "éa" = 3 bytes (é=2, a=1), 2 runes; 3000 repeats = 9000 bytes, 6000 runes; 1333 units = 3999 bytes, so byte 4000 lands mid-é
			maxLen: 4000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capSpeechText(tt.input)
			wantSuffix := "… (truncated)"

			if !strings.HasSuffix(got, wantSuffix) {
				t.Fatalf("capSpeechText result missing suffix, got tail: %q", got[len(got)-30:])
			}

			body := strings.TrimSuffix(got, wantSuffix)
			runeCount := len([]rune(body))
			if runeCount != tt.maxLen {
				t.Errorf("body rune count = %d, want %d", runeCount, tt.maxLen)
			}

			if !utf8.ValidString(body) {
				t.Errorf("body is not valid UTF-8")
			}

			inputRunes := []rune(tt.input)
			expectedBody := string(inputRunes[:tt.maxLen])
			if body != expectedBody {
				t.Errorf("body = %q, want %q (first %d runes of input)", body, expectedBody, tt.maxLen)
			}
		})
	}
}

func TestSpeakNilWhenUnconfigured(t *testing.T) {
	c := newTestClients(t, `
shell3.model("m", { base_url = "http://x", model = "id" })
`+baseAgent, nil)
	if c.Speak != nil {
		t.Fatal("want nil Speak when shell3.tts is absent")
	}
}

func TestSpeakEmptyTextErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for empty text")
	}))
	defer srv.Close()

	c := newSpeakClients(t, srv.URL, "opus")
	_, err := c.Speak(context.Background(), "**  **")
	if err == nil {
		t.Fatal("want error for empty stripped text")
	}
	if !strings.Contains(err.Error(), "nothing to speak") {
		t.Errorf("error = %v, want mention of 'nothing to speak'", err)
	}
}
