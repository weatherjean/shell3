package openai

import (
	"context"
	"fmt"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

type provider struct{}

func init() { llm.Register("openai", &provider{}) }

func (*provider) Name() string         { return "openai" }
func (*provider) SingleInstance() bool { return false }

// NewClient reads the instance from store and builds a Client.
func (*provider) NewClient(_ context.Context, store *config.AuthStore, instance, model string) (llm.Streamer, error) {
	inst, ok := store.Get(instance)
	if !ok {
		return nil, fmt.Errorf("openai: no instance %q — edit ~/.shell3/ai-do-not-read.auth.yaml", instance)
	}
	if model == "" && len(inst.Models) > 0 {
		model = inst.Models[0].ID
	}
	return NewClient(inst.BaseURL, inst.APIKey, model), nil
}
