package codex

import (
	"reflect"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestProviderModels_DefaultIncludesGPT55(t *testing.T) {
	p := &provider{}
	models := p.Models(nil, "codex")

	found := false
	for _, m := range models {
		if m == "gpt-5.5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("models = %v, want to include gpt-5.5", models)
	}
}

func TestProviderModels_UsesStoredDefaultModelCSV(t *testing.T) {
	home := t.TempDir()
	store, err := config.LoadCredStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("codex", "codex", map[string]string{
		"default_model": "gpt-5.5,gpt-5.1-codex-mini",
	}); err != nil {
		t.Fatal(err)
	}

	p := &provider{}
	got := p.Models(store, "codex")
	want := []string{"gpt-5.5", "gpt-5.1-codex-mini"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Models() = %v, want %v", got, want)
	}
}
