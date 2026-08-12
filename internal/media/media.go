//go:build unix

// Package media implements shell3's OpenAI-compatible media capabilities
// (transcribe) as a thin openai-go wrapper resolved
// from shell3.yaml's media: block (stt).
package media

import (
	"context"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/mediadir"
)

// Config is the read-only slice of the loaded config that the
// media capabilities need. *config.LoadedConfig satisfies it structurally;
// callers pass that concrete type without either package importing the
// other's wider surface.
type Config interface {
	STT() *config.STTConfig
	Model(name string) (config.Model, bool)
}

// Clients holds shell3's media capabilities, each wired to the model its
// shell3.yaml block references. A nil function field means the capability was
// not configured (no media stt block); callers check
// for nil before use rather than calling into a stub that errors.
type Clients struct {
	Transcribe func(ctx context.Context, path string) (string, error)
}

// sdkFn resolves an openai-go client for a media block's model ref (the
// "model" field, naming a shell3.model declaration) and returns the
// resolved config.Model alongside it. Model refs are validated at config load
// time, so the lookup here cannot miss. It is plain client construction —
// proxy-spawning is layered on top by sdkOnce, not baked in here, so it can
// be shared across both capabilities.
type sdkFn func(ref string) (openai.Client, config.Model)

// sdkOnce runs ensureProxy for ref's model exactly once — guarded by once,
// which the caller owns per capability — then resolves the client via sdk.
// Deferring the proxy spawn to first use (rather than spawning eagerly for
// every configured capability in New) avoids starting a run_proxy command for
// a capability a session never invokes.
func sdkOnce(once *sync.Once, ensureProxy func(name, command string), sdk sdkFn, ref string) (openai.Client, config.Model) {
	client, m := sdk(ref)
	once.Do(func() { ensureProxy(m.Name, m.RunProxy) })
	return client, m
}

// New builds Clients from cfg. ensureProxy is called at most once per
// capability, on that capability's first use, as (model name, run_proxy
// command); pass modelproxy.Spawner.Ensure in production (itself idempotent
// per model name) or a no-op in tests. Unconfigured capabilities (no
// matching shell3.yaml block) leave their function field nil.
func New(cfg Config, ensureProxy func(name, command string)) *Clients {
	c := &Clients{}

	// sdk is the shared, proxy-agnostic client resolver; each capability
	// wraps it with its own sync.Once via sdkOnce below.
	sdk := func(ref string) (openai.Client, config.Model) {
		m, _ := cfg.Model(ref)
		return openai.NewClient(option.WithBaseURL(m.BaseURL), option.WithAPIKey(m.APIKey)), m
	}

	if s := cfg.STT(); s != nil {
		var once sync.Once
		c.Transcribe = newTranscriber(func(ref string) (openai.Client, config.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *s)
	}
	return c
}

// Dir returns shell3's durable media directory — where attachments are
// stored, so every media file the agent has seen keeps a stable path that
// survives reboots and OS temp cleaning (re-readable with read_media,
// re-sendable to the chat, findable from history). Default <configDir>/media
// (which is ~/.shell3/media for the default config dir, see
// mediadir.SetBaseDir); $SHELL3_MEDIA_DIR overrides (tests point it at a
// TempDir). Created on demand.
func Dir() (string, error) {
	return mediadir.Dir()
}
