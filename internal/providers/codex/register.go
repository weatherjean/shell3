package codex

import (
	"context"
	"io"

	"github.com/weatherjean/shell3/internal/llm"
)

// provider is the llm.Provider impl for Codex.
type provider struct{}

func init() {
	llm.Register("codex", &provider{})
}

// Auth runs the PKCE OAuth flow against auth.openai.com and saves tokens.
func (*provider) Auth(ctx context.Context, w io.Writer) error {
	home, err := homeDir()
	if err != nil {
		return err
	}
	_, err = runBrowserFlow(ctx, home, w)
	return err
}

// NewClient builds a Streamer backed by the Codex Responses API.
func (*provider) NewClient(ctx context.Context, model string) (llm.Streamer, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	_ = ctx
	return newClient(home, model)
}

// Models lists the Codex models exposed via the ChatGPT subscription tier.
// Refresh when OpenAI updates the official Codex CLI's allowed list.
func (*provider) Models() []string {
	return []string{
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.2",
		"gpt-5.3-codex",
		"gpt-5.4",
	}
}
