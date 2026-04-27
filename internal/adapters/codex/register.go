package codex

import (
	"context"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

type provider struct{}

func init() { llm.Register("codex", &provider{}) }

func (*provider) Name() string         { return "codex" }
func (*provider) SingleInstance() bool { return true }

// Auth runs the PKCE OAuth flow and persists tokens via store.
func (*provider) Auth(ctx context.Context, w io.Writer, store *config.CredStore, instance string) error {
	_ = instance
	_, err := runBrowserFlow(ctx, store, w)
	return err
}

// NewClient builds a Streamer backed by the Codex Responses API.
func (*provider) NewClient(ctx context.Context, store *config.CredStore, instance, model string) (llm.Streamer, error) {
	_ = instance
	_ = ctx
	return newClient(store, model)
}

// Models lists the Codex models exposed via the ChatGPT subscription tier.
// Honors the user's stored default_model CSV when present (set via
// `shell3 auth --provider-models=codex`); otherwise falls back to the
// hardcoded list.
func (*provider) Models(store *config.CredStore, instance string) []string {
	if store != nil {
		if _, fields, ok := store.Get(instance); ok {
			out := []string{}
			for _, m := range strings.Split(fields["default_model"], ",") {
				if m := strings.TrimSpace(m); m != "" {
					out = append(out, m)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return []string{
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.2",
		"gpt-5.3-codex",
		"gpt-5.4",
	}
}
