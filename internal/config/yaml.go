package config

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// yamlFile is the wire schema of shell3.yaml. Decoding is strict
// (KnownFields): an unknown key anywhere is a load error.
type yamlFile struct {
	Models        map[string]yamlModel `yaml:"models"`
	Web           *yamlWeb             `yaml:"web"`
	MCP           map[string]yamlMCP   `yaml:"mcp"`
	Media         *yamlMedia           `yaml:"media"`
	Background    *yamlBackground      `yaml:"background"`
	RunsKeepDays  *int                 `yaml:"runs_keep_days"`  // nil = default 30; 0 = keep forever
	MediaKeepDays *int                 `yaml:"media_keep_days"` // nil = default 0 = keep forever
}

type yamlModel struct {
	BaseURL       string         `yaml:"base_url"`
	APIKey        string         `yaml:"api_key"`
	Model         string         `yaml:"model"`
	ContextWindow int            `yaml:"context_window"`
	CompactAt     int            `yaml:"compact_at"`
	KeepRecent    int            `yaml:"keep_recent"`
	PruneAt       *int           `yaml:"prune_at"` // nil = derive from compact_at; 0 = disabled
	Reasoning     string         `yaml:"reasoning"`
	MaxTokens     int            `yaml:"max_tokens"`
	Temperature   *float64       `yaml:"temperature"`
	Extra         map[string]any `yaml:"extra"`
	RunProxy      string         `yaml:"run_proxy"`
}

// yamlWeb is the `web:` block: where the agent's shell runs, and how the
// interface is served.
type yamlWeb struct {
	WorkDir    string `yaml:"workdir"`
	Addr       string `yaml:"addr"`
	URL        string `yaml:"url"`
	Password   string `yaml:"password"`
	TOTPSecret string `yaml:"totp_secret"`
}

type yamlMCP struct {
	Command []string          `yaml:"command"`
	Env     map[string]string `yaml:"env"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Timeout int               `yaml:"timeout"`
	Allow   []string          `yaml:"allow"`
	Deny    []string          `yaml:"deny"`
}

type yamlMedia struct {
	STT      *yamlSTT      `yaml:"stt"`
	TTS      *yamlTTS      `yaml:"tts"`
	Describe *yamlDescribe `yaml:"describe"`
	Imagegen *yamlImagegen `yaml:"imagegen"`
}

type yamlSTT struct {
	Model    string `yaml:"model"`
	Language string `yaml:"language"`
	Echo     bool   `yaml:"echo"`
}

type yamlTTS struct {
	Model  string `yaml:"model"`
	Voice  string `yaml:"voice"`
	Mode   string `yaml:"mode"`
	Format string `yaml:"format"`
}

type yamlDescribe struct {
	Model  string `yaml:"model"`
	Prompt string `yaml:"prompt"`
}

type yamlImagegen struct {
	Model string `yaml:"model"`
	Size  string `yaml:"size"`
	API   string `yaml:"api"`
}

type yamlBackground struct {
	MaxConcurrent int `yaml:"max_concurrent"`
}

var mcpNameRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// parseYAML strict-decodes shell3.yaml, resolves env: references from
// secrets, and fills the wiring fields of c.
func (c *LoadedConfig) parseYAML(data []byte, secrets map[string]string) error {
	var f yamlFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return fmt.Errorf("shell3.yaml: %w", err)
	}
	if err := resolveEnvRefs(&f, secrets); err != nil {
		return fmt.Errorf("shell3.yaml: %w", err)
	}
	if len(f.Models) == 0 {
		return fmt.Errorf("shell3.yaml: no models declared")
	}
	names := make([]string, 0, len(f.Models))
	for name := range f.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m := f.Models[name]
		if m.BaseURL == "" || m.Model == "" {
			return fmt.Errorf("shell3.yaml: model %q needs base_url and model", name)
		}
		// prune_at defaults to compact_at*0.6 so the cheap-prune tier is on by
		// default wherever compaction is; an explicit value at or above
		// compact_at is clamped to 0 (disabled) rather than firing after it.
		// Both tiers key off compact_at at runtime, so an explicit prune_at
		// without compact_at would be silently dead — reject it instead.
		if m.PruneAt != nil && *m.PruneAt > 0 && m.CompactAt <= 0 {
			return fmt.Errorf("shell3.yaml: model %q sets prune_at without compact_at (pruning only runs while compaction is armed)", name)
		}
		var pruneAt int
		switch {
		case m.PruneAt == nil && m.CompactAt > 0:
			pruneAt = m.CompactAt * 60 / 100
		case m.PruneAt != nil && *m.PruneAt < m.CompactAt:
			pruneAt = *m.PruneAt
		}
		// A keep_recent at or above compact_at would preserve a verbatim tail
		// bigger than the trigger, so compaction could never get back under the
		// threshold and would re-fire every turn; clamp it to half.
		keepRecent := m.KeepRecent
		if m.CompactAt > 0 && keepRecent >= m.CompactAt {
			keepRecent = m.CompactAt / 2
		}
		c.Models = append(c.Models, Model{
			Name: name, BaseURL: m.BaseURL, APIKey: m.APIKey, ModelID: m.Model,
			ContextWindow: m.ContextWindow, CompactAt: m.CompactAt,
			KeepRecent: keepRecent, PruneAt: pruneAt,
			Reasoning: m.Reasoning, MaxTokens: m.MaxTokens,
			Temperature: m.Temperature, Extra: m.Extra, RunProxy: m.RunProxy,
		})
	}
	if wc := f.Web; wc != nil {
		c.web = WebConfig{WorkDir: wc.WorkDir, Addr: wc.Addr, URL: wc.URL, Password: wc.Password,
			TOTPSecret: wc.TOTPSecret}
	}
	mcpNames := make([]string, 0, len(f.MCP))
	for name := range f.MCP {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		s := f.MCP[name]
		if !mcpNameRE.MatchString(name) {
			return fmt.Errorf("shell3.yaml: mcp server name %q must match %s", name, mcpNameRE)
		}
		if (len(s.Command) == 0) == (s.URL == "") {
			return fmt.Errorf("shell3.yaml: mcp server %q needs exactly one of command or url", name)
		}
		if len(s.Allow) > 0 && len(s.Deny) > 0 {
			return fmt.Errorf("shell3.yaml: mcp server %q: set at most one of allow/deny", name)
		}
		c.mcpServers = append(c.mcpServers, MCPServer{
			Name: name, Command: s.Command, Env: s.Env, URL: s.URL,
			Headers: s.Headers, TimeoutSecs: s.Timeout, Allow: s.Allow, Deny: s.Deny,
		})
	}
	if m := f.Media; m != nil {
		if s := m.STT; s != nil {
			if s.Model == "" {
				return fmt.Errorf("shell3.yaml: media.stt needs a model")
			}
			c.stt = &STTConfig{ModelRef: s.Model, Language: s.Language, Echo: s.Echo}
		}
		if tt := m.TTS; tt != nil {
			if tt.Model == "" {
				return fmt.Errorf("shell3.yaml: media.tts needs a model")
			}
			mode := tt.Mode
			if mode == "" {
				mode = "inbound"
			}
			if mode != "off" && mode != "inbound" && mode != "always" {
				return fmt.Errorf("shell3.yaml: media.tts mode %q must be off, inbound, or always", tt.Mode)
			}
			// opus is small and widely playable in the browser; the default.
			format := tt.Format
			if format == "" {
				format = "opus"
			}
			c.tts = &TTSConfig{ModelRef: tt.Model, Voice: tt.Voice, Mode: mode, Format: format}
		}
		if d := m.Describe; d != nil {
			if d.Model == "" {
				return fmt.Errorf("shell3.yaml: media.describe needs a model")
			}
			prompt := d.Prompt
			if prompt == "" {
				prompt = "Describe the image."
			}
			c.describe = &DescribeConfig{ModelRef: d.Model, Prompt: prompt}
		}
		if ig := m.Imagegen; ig != nil {
			if ig.Model == "" {
				return fmt.Errorf("shell3.yaml: media.imagegen needs a model")
			}
			api := ig.API
			if api == "" {
				api = "openai"
			}
			if api != "openai" && api != "openrouter" {
				return fmt.Errorf("shell3.yaml: media.imagegen api %q must be openai or openrouter", ig.API)
			}
			size := ig.Size
			if size == "" {
				size = "1024x1024"
			}
			c.imagegen = &ImagegenConfig{ModelRef: ig.Model, Size: size, API: api}
		}
	}
	if b := f.Background; b != nil {
		c.BackgroundMaxConcurrent = b.MaxConcurrent
	}
	// runs_keep_days defaults to 30 (unset); an explicit 0 means keep
	// forever, so the default can't be expressed as a bare int default.
	c.RunsKeepDays = 30
	if f.RunsKeepDays != nil {
		c.RunsKeepDays = *f.RunsKeepDays
	}
	if err := validateKeepDays("runs_keep_days", c.RunsKeepDays); err != nil {
		return err
	}
	// media_keep_days defaults to 0 = keep forever: delivered files and
	// uploads are user data, so deletion is opt-in rather than assumed.
	c.MediaKeepDays = 0
	if f.MediaKeepDays != nil {
		c.MediaKeepDays = *f.MediaKeepDays
	}
	if err := validateKeepDays("media_keep_days", c.MediaKeepDays); err != nil {
		return err
	}
	return nil
}

// maxKeepDays bounds runs_keep_days/media_keep_days at load time. Both
// values eventually feed `time.Duration(days) * 24 * time.Hour`, which
// overflows int64 nanoseconds past ~106751 days and can wrap around to a
// small POSITIVE duration — silently inverting "keep basically forever"
// into "delete almost everything" the next time the janitor runs. 100 years
// is nowhere near that wraparound and is already an absurd retention
// window, so anything above it is almost certainly a fat-finger, not intent.
const maxKeepDays = 36500

// validateKeepDays rejects a keep-days value that is negative (silently
// meaning "keep forever" is confusing when the janitor's own zero already
// means that) or large enough to risk the overflow above.
func validateKeepDays(key string, days int) error {
	if days < 0 {
		return fmt.Errorf("shell3.yaml: %s must not be negative (use 0 to keep forever); got %d", key, days)
	}
	if days > maxKeepDays {
		return fmt.Errorf("shell3.yaml: %s is too large (max %d days); got %d", key, maxKeepDays, days)
	}
	return nil
}

var envRefRe = regexp.MustCompile(`env:([A-Za-z_][A-Za-z0-9_]*)`)

// resolveEnvRefs walks v (a pointer to a decoded wire struct) and substitutes
// every env:KEY token inside every string — including strings in maps and
// slices — from secrets. An env:KEY naming a key absent from .env is an
// error, so a typo'd reference can never silently become the literal text.
func resolveEnvRefs(v any, secrets map[string]string) error {
	return walkStrings(reflect.ValueOf(v), func(s string) (string, error) {
		var rerr error
		out := envRefRe.ReplaceAllStringFunc(s, func(tok string) string {
			key := tok[len("env:"):]
			val, ok := secrets[key]
			if !ok {
				rerr = fmt.Errorf("env:%s not found in .env", key)
				return tok
			}
			return val
		})
		return out, rerr
	})
}

// walkStrings applies fn to every string reachable from v through pointers,
// structs, maps, slices, and interfaces (map keys are left alone). Map values
// and interface contents are not addressable, so they are copy-walked and set
// back.
func walkStrings(v reflect.Value, fn func(string) (string, error)) error {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return walkStrings(v.Elem(), fn)
	case reflect.Interface:
		if v.IsNil() || !v.CanSet() {
			return nil
		}
		cp := reflect.New(v.Elem().Type()).Elem()
		cp.Set(v.Elem())
		if err := walkStrings(cp, fn); err != nil {
			return err
		}
		v.Set(cp)
	case reflect.Struct:
		for i := range v.NumField() {
			if err := walkStrings(v.Field(i), fn); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			cp := reflect.New(v.MapIndex(k).Type()).Elem()
			cp.Set(v.MapIndex(k))
			if err := walkStrings(cp, fn); err != nil {
				return err
			}
			v.SetMapIndex(k, cp)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := walkStrings(v.Index(i), fn); err != nil {
				return err
			}
		}
	case reflect.String:
		if !v.CanSet() {
			return nil
		}
		out, err := fn(v.String())
		if err != nil {
			return err
		}
		v.SetString(out)
	}
	return nil
}
