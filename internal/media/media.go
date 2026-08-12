//go:build unix

// Package media implements shell3's OpenAI-compatible media capabilities
// (transcribe, speak, describe) as thin openai-go wrappers resolved
// from shell3.yaml's media: blocks (stt/tts/describe).
package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	TTS() *config.TTSConfig
	Describe() *config.DescribeConfig
	Model(name string) (config.Model, bool)
}

// Speech is a synthesized-audio result from Clients.Speak: the path to the
// written audio file. The caller owns it (the telegram front-end sends it as
// a voice note; cached files live under the media dir as tts-*).
type Speech struct {
	Path string
}

// Clients holds shell3's media capabilities, each wired to the model its
// shell3.yaml block references. A nil function field means the capability was
// not configured (no media stt/tts/describe block); callers check
// for nil before use rather than calling into a stub that errors.
type Clients struct {
	Transcribe func(ctx context.Context, path string) (string, error)
	Speak      func(ctx context.Context, text string) (Speech, error)
	Describe   func(ctx context.Context, path string) (string, error)
}

// sdkFn resolves an openai-go client for a media block's model ref (the
// "model" field, naming a shell3.model declaration) and returns the
// resolved config.Model alongside it. Model refs are validated at config load
// time, so the lookup here cannot miss. It is plain client construction —
// proxy-spawning is layered on top by sdkOnce, not baked in here, so it can
// be shared across all four capabilities.
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
	if t := cfg.TTS(); t != nil {
		var once sync.Once
		c.Speak = newSpeaker(func(ref string) (openai.Client, config.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *t)
	}
	if d := cfg.Describe(); d != nil {
		var once sync.Once
		c.Describe = newDescriber(func(ref string) (openai.Client, config.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *d)
	}
	return c
}

// Dir returns shell3's durable media directory — where attachments,
// generated images, and cached speech are stored, so every media file the
// agent has seen or made keeps a stable path that survives reboots and OS
// temp cleaning (re-readable with read_media, re-sendable to the chat,
// findable from history). Default <configDir>/media (which is
// ~/.shell3/media for the default config dir, see mediadir.SetBaseDir);
// $SHELL3_MEDIA_DIR overrides (tests point it at a TempDir). Created on
// demand.
func Dir() (string, error) {
	return mediadir.Dir()
}

// outDir returns shell3's transient media scratch directory, used for
// freshly synthesized TTS audio before the front-end caches it into Dir()
// under a content hash. Generated images go straight to Dir().
func outDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "shell3-media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("media: cannot create %s: %w", dir, err)
	}
	return dir, nil
}
