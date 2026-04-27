package openai

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

// provider is the llm.Provider impl for the OpenAI-compatible adapter.
// stdin is parameterized for tests; nil falls back to os.Stdin.
type provider struct {
	stdin io.Reader
}

func init() { llm.Register("openai", &provider{}) }

func (*provider) Name() string         { return "openai" }
func (*provider) SingleInstance() bool { return false }

// NewClient reads the instance's fields from store and builds a Client.
func (*provider) NewClient(_ context.Context, store *config.CredStore, instance, model string) (llm.Streamer, error) {
	adapter, fields, ok := store.Get(instance)
	if !ok {
		return nil, fmt.Errorf("openai: no instance %q — run: shell3 auth", instance)
	}
	if adapter != "openai" {
		return nil, fmt.Errorf("openai: instance %q has adapter %q", instance, adapter)
	}
	if model == "" {
		model = firstModel(fields["default_model"])
	}
	return NewClient(fields["base_url"], fields["api_key"], model), nil
}

// Models returns the comma-separated default_model list.
func (*provider) Models(store *config.CredStore, instance string) []string {
	_, fields, ok := store.Get(instance)
	if !ok {
		return nil
	}
	out := []string{}
	for _, m := range strings.Split(fields["default_model"], ",") {
		if m := strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func firstModel(csv string) string {
	for _, m := range strings.Split(csv, ",") {
		if m := strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return ""
}
