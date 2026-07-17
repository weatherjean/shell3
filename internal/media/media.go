//go:build unix

// Package media implements shell3's OpenAI-compatible media capabilities
// (transcribe, speak, describe, generate) as thin openai-go wrappers resolved
// from shell3.lua's media blocks (shell3.stt/tts/describe/imagegen).
package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/weatherjean/shell3/internal/luacfg"
)

// Config is the read-only slice of the loaded shell3.lua config that the
// media capabilities need. *luacfg.LoadedConfig satisfies it structurally;
// callers pass that concrete type without either package importing the
// other's wider surface.
type Config interface {
	STT() *luacfg.STTConfig
	TTS() *luacfg.TTSConfig
	Describe() *luacfg.DescribeConfig
	Imagegen() *luacfg.ImagegenConfig
	Model(name string) (luacfg.Model, bool)
}

// Speech is a synthesized-audio result from Clients.Speak.
type Speech struct {
	Path string
	// VoiceCompatible is true when Path's codec (opus/ogg) can be sent as a
	// Telegram voice bubble rather than a plain audio document.
	VoiceCompatible bool
}

// Clients holds shell3's media capabilities, each wired to the model its
// shell3.lua block references. A nil function field means the capability was
// not configured (no shell3.stt/tts/describe/imagegen block); callers check
// for nil before use rather than calling into a stub that errors.
type Clients struct {
	Transcribe func(ctx context.Context, path string) (string, error)
	Speak      func(ctx context.Context, text string) (Speech, error)
	Describe   func(ctx context.Context, path string) (string, error)
	Generate   func(ctx context.Context, prompt, size string) (string, error)

	// STTEcho mirrors shell3.stt{}.echo: whether the transcript is echoed
	// back to the chat before the model turn runs.
	STTEcho bool
	// TTSMode mirrors shell3.tts{}.mode: the configured default
	// ("off"/"inbound"/"always") for when outbound replies are synthesized.
	TTSMode string
	// GenSize mirrors shell3.imagegen{}.size: the default requested
	// dimensions for Generate when a caller doesn't override it.
	GenSize string
}

// sdkFn resolves an openai-go client for a media block's model ref (the
// "model" field, naming a shell3.model declaration) and returns the
// resolved luacfg.Model alongside it. Model refs are validated at config load
// time, so the lookup here cannot miss. It is plain client construction —
// proxy-spawning is layered on top by sdkOnce, not baked in here, so it can
// be shared across all four capabilities.
type sdkFn func(ref string) (openai.Client, luacfg.Model)

// sdkOnce runs ensureProxy for ref's model exactly once — guarded by once,
// which the caller owns per capability — then resolves the client via sdk.
// Deferring the proxy spawn to first use (rather than spawning eagerly for
// every configured capability in New) avoids starting a run_proxy command for
// a capability a session never invokes.
func sdkOnce(once *sync.Once, ensureProxy func(name, command string), sdk sdkFn, ref string) (openai.Client, luacfg.Model) {
	client, m := sdk(ref)
	once.Do(func() { ensureProxy(m.Name, m.RunProxy) })
	return client, m
}

// New builds Clients from cfg. ensureProxy is called at most once per
// capability, on that capability's first use, as (model name, run_proxy
// command); pass modelproxy.Spawner.Ensure in production (itself idempotent
// per model name) or a no-op in tests. Unconfigured capabilities (no
// matching shell3.lua block) leave their function field nil.
func New(cfg Config, ensureProxy func(name, command string)) *Clients {
	c := &Clients{}

	// sdk is the shared, proxy-agnostic client resolver; each capability
	// wraps it with its own sync.Once via sdkOnce below.
	sdk := func(ref string) (openai.Client, luacfg.Model) {
		m, _ := cfg.Model(ref)
		return openai.NewClient(option.WithBaseURL(m.BaseURL), option.WithAPIKey(m.APIKey)), m
	}

	if s := cfg.STT(); s != nil {
		c.STTEcho = s.Echo
		var once sync.Once
		c.Transcribe = newTranscriber(func(ref string) (openai.Client, luacfg.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *s)
	}
	if t := cfg.TTS(); t != nil {
		c.TTSMode = t.Mode
		var once sync.Once
		c.Speak = newSpeaker(func(ref string) (openai.Client, luacfg.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *t)
	}
	if d := cfg.Describe(); d != nil {
		var once sync.Once
		c.Describe = newDescriber(func(ref string) (openai.Client, luacfg.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *d)
	}
	if ig := cfg.Imagegen(); ig != nil {
		c.GenSize = ig.Size
		var once sync.Once
		c.Generate = newGenerator(func(ref string) (openai.Client, luacfg.Model) {
			return sdkOnce(&once, ensureProxy, sdk, ref)
		}, *ig)
	}
	return c
}

// Dir returns shell3's durable media directory — where generated images and
// inbound Telegram attachments are stored, so every media file the agent has
// seen or made keeps a stable path that survives reboots and OS temp
// cleaning (re-readable with read_media, re-sendable, findable from
// history). Default ~/.shell3/media; $SHELL3_MEDIA_DIR overrides (tests
// point it at a TempDir). Created on demand. Synthesized TTS audio does NOT
// live here — it is sent and deleted in the same breath (see outDir).
func Dir() (string, error) {
	dir := os.Getenv("SHELL3_MEDIA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("media: resolving home dir: %w", err)
		}
		dir = filepath.Join(home, ".shell3", "media")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("media: cannot create %s: %w", dir, err)
	}
	return dir, nil
}

// outDir returns shell3's transient media scratch directory, now used only
// for synthesized TTS audio (sent to the chat and deleted immediately —
// durable storage would be noise). Generated images go to Dir() instead.
func outDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "shell3-media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("media: cannot create %s: %w", dir, err)
	}
	return dir, nil
}
