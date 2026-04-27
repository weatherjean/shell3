package openai

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestProviderAuth_PromptsAndPersists(t *testing.T) {
	home := t.TempDir()
	store, err := config.LoadCredStore(home)
	if err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(strings.Join([]string{
		"http://localhost:11434/v1",
		"",
		"llama3.2",
		"",
	}, "\n"))
	var out bytes.Buffer
	p := &provider{stdin: in}
	if err := p.Auth(context.Background(), &out, store, "ollama-local"); err != nil {
		t.Fatalf("Auth: %v", err)
	}
	adapter, fields, ok := store.Get("ollama-local")
	if !ok {
		t.Fatal("instance not persisted")
	}
	if adapter != "openai" {
		t.Fatalf("adapter=%q want openai", adapter)
	}
	if fields["base_url"] != "http://localhost:11434/v1" || fields["default_model"] != "llama3.2" {
		t.Fatalf("fields=%v", fields)
	}
	if !strings.Contains(out.String(), "Configure an OpenAI-compatible") {
		t.Fatalf("missing header in output:\n%s", out.String())
	}
}
