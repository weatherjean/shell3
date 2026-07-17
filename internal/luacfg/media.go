package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// STTConfig is the parsed shell3.stt{} block: speech-to-text for inbound
// voice messages. Echo controls whether the transcript is echoed back to the
// user before the model turn runs.
type STTConfig struct {
	ModelRef, Language string
	Echo               bool
}

// TTSConfig is the parsed shell3.tts{} block: text-to-speech for outbound
// replies. Mode governs when synthesis runs ("off", "inbound" — only reply to
// a voice message with voice — or "always"); Format is the output codec.
type TTSConfig struct{ ModelRef, Voice, Mode, Format string }

// DescribeConfig is the parsed shell3.describe{} block: captions an inbound
// image before the model turn runs. Prompt is the instruction sent with the
// image.
type DescribeConfig struct{ ModelRef, Prompt string }

// ImagegenConfig is the parsed shell3.imagegen{} block: image generation.
// Size is the requested output dimensions. API selects the wire shape used
// to talk to the model's base_url ("openai" — the SDK's Images.Generate — or
// "openrouter" — a raw POST to <base_url>/images").
type ImagegenConfig struct{ ModelRef, Size, API string }

// STT returns the parsed shell3.stt{} block, nil when not declared.
func (c *LoadedConfig) STT() *STTConfig { return c.stt }

// TTS returns the parsed shell3.tts{} block, nil when not declared.
func (c *LoadedConfig) TTS() *TTSConfig { return c.tts }

// Describe returns the parsed shell3.describe{} block, nil when not declared.
func (c *LoadedConfig) Describe() *DescribeConfig { return c.describe }

// Imagegen returns the parsed shell3.imagegen{} block, nil when not declared.
func (c *LoadedConfig) Imagegen() *ImagegenConfig { return c.imagegen }

var sttKeys = map[string]bool{"model": true, "language": true, "echo": true}
var ttsKeys = map[string]bool{"model": true, "voice": true, "mode": true, "format": true}
var describeKeys = map[string]bool{"model": true, "prompt": true}
var imagegenKeys = map[string]bool{"model": true, "size": true, "api": true}

// reqModelRef reads the required model field of a media block: the name of a
// shell3.model declaration the block will use. Resolution to an actual Model
// happens later, post-parse, in validateMediaRefs — so declaration order
// between the media block and its shell3.model does not matter.
func reqModelRef(L *lua.LState, t *lua.LTable, block string) string {
	ref := optStr(t, "model")
	if ref == "" {
		L.RaiseError("%s: model is required (the name of a shell3.model declaration)", block)
	}
	return ref
}

// luaSTT parses shell3.stt({ model=..., language=..., echo=... }). Exactly
// one declaration is allowed.
func (c *LoadedConfig) luaSTT(L *lua.LState) int {
	if c.stt != nil {
		L.RaiseError("stt: only one shell3.stt may be declared")
	}
	opts := L.CheckTable(1)
	mustKeys(L, opts, "stt", sttKeys)
	s := &STTConfig{ModelRef: reqModelRef(L, opts, "stt"), Language: optStr(opts, "language"), Echo: true}
	if opts.RawGetString("echo") != lua.LNil {
		s.Echo = optBool(opts, "echo")
	}
	c.stt = s
	return 0
}

// luaTTS parses shell3.tts({ model=..., voice=..., mode=..., format=... }).
// Exactly one declaration is allowed.
func (c *LoadedConfig) luaTTS(L *lua.LState) int {
	if c.tts != nil {
		L.RaiseError("tts: only one shell3.tts may be declared")
	}
	opts := L.CheckTable(1)
	mustKeys(L, opts, "tts", ttsKeys)
	t := &TTSConfig{
		ModelRef: reqModelRef(L, opts, "tts"),
		Voice:    optStr(opts, "voice"),
		Mode:     optStr(opts, "mode"),
		Format:   optStr(opts, "format"),
	}
	if t.Mode == "" {
		t.Mode = "inbound"
	}
	if t.Mode != "off" && t.Mode != "inbound" && t.Mode != "always" {
		L.RaiseError("tts: mode %q must be off, inbound, or always", t.Mode)
	}
	if t.Format == "" {
		t.Format = "opus"
	}
	c.tts = t
	return 0
}

// luaDescribe parses shell3.describe({ model=..., prompt=... }). Exactly one
// declaration is allowed.
func (c *LoadedConfig) luaDescribe(L *lua.LState) int {
	if c.describe != nil {
		L.RaiseError("describe: only one shell3.describe may be declared")
	}
	opts := L.CheckTable(1)
	mustKeys(L, opts, "describe", describeKeys)
	d := &DescribeConfig{ModelRef: reqModelRef(L, opts, "describe"), Prompt: optStr(opts, "prompt")}
	if d.Prompt == "" {
		d.Prompt = "Describe the image."
	}
	c.describe = d
	return 0
}

// luaImagegen parses shell3.imagegen({ model=..., size=... }). Exactly one
// declaration is allowed.
func (c *LoadedConfig) luaImagegen(L *lua.LState) int {
	if c.imagegen != nil {
		L.RaiseError("imagegen: only one shell3.imagegen may be declared")
	}
	opts := L.CheckTable(1)
	mustKeys(L, opts, "imagegen", imagegenKeys)
	ig := &ImagegenConfig{ModelRef: reqModelRef(L, opts, "imagegen"), Size: optStr(opts, "size"), API: optStr(opts, "api")}
	if ig.Size == "" {
		ig.Size = "1024x1024"
	}
	if ig.API == "" {
		ig.API = "openai"
	}
	if ig.API != "openai" && ig.API != "openrouter" {
		L.RaiseError("imagegen: api %q must be openai or openrouter", ig.API)
	}
	c.imagegen = ig
	return 0
}

// validateMediaRefs checks that each declared media block's model reference
// resolves to a declared shell3.model. It runs post-parse (see Load) so
// declaration order between a media block and its shell3.model never matters.
func (c *LoadedConfig) validateMediaRefs() error {
	check := func(block, ref string) error {
		if ref == "" {
			return nil
		}
		if _, ok := c.Model(ref); !ok {
			return fmt.Errorf("config: %s: model %q is not declared (add shell3.model(%q, ...))", block, ref, ref)
		}
		return nil
	}
	if c.stt != nil {
		if err := check("stt", c.stt.ModelRef); err != nil {
			return err
		}
	}
	if c.tts != nil {
		if err := check("tts", c.tts.ModelRef); err != nil {
			return err
		}
	}
	if c.describe != nil {
		if err := check("describe", c.describe.ModelRef); err != nil {
			return err
		}
	}
	if c.imagegen != nil {
		if err := check("imagegen", c.imagegen.ModelRef); err != nil {
			return err
		}
	}
	return nil
}
