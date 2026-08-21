package config

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlFile is the wire schema of the kit's `shell3:` block. Decoding is strict
// (KnownFields): an unknown key anywhere is a load error.
type yamlFile struct {
	Models        map[string]yamlModel `yaml:"models"`
	Telegram      *yamlTelegram        `yaml:"telegram"`
	MCP           map[string]yamlMCP   `yaml:"mcp"`
	Background    *yamlBackground      `yaml:"background"`
	RunsKeepDays  *int                 `yaml:"runs_keep_days"`  // nil = default 30; 0 = keep forever
	DashPort      *int                 `yaml:"dash_port"`       // nil = default 7333; 0 = dash disabled
	ReviewModel   string               `yaml:"review_model"`    // "" = the main agent's model
	ReviewPolicy  string               `yaml:"review_policy"`   // operator rules for the {review} guardian
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

// yamlTelegram is the `telegram:` block: the front-end's bot credentials and
// where the agent's shell runs. Token is a secret resolved from .env via an
// env:KEY reference.
type yamlTelegram struct {
	Token     string   `yaml:"token"`
	ChatID    string   `yaml:"chat_id"`
	WorkDir   string   `yaml:"workdir"`
	AllowFrom []string `yaml:"allow_from"`
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

type yamlBackground struct {
	MaxConcurrent int `yaml:"max_concurrent"`
}

var mcpNameRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// wiringLabel prefixes every wiring error: the YAML being decoded is the kit's
// `shell3:` declaration block, so naming a file the operator does not have
// would send them looking for the wrong thing.
const wiringLabel = KitFileName + " shell3: block"

// yamlTypeNames maps the wire structs onto the wiring blocks they decode, so a
// strict-decode failure reads as config rather than as Go.
var yamlTypeNames = map[string]string{
	"yamlFile":       "the shell3: block",
	"yamlModel":      "a models: entry",
	"yamlTelegram":   "the telegram: block",
	"yamlMCP":        "an mcp: server",
	"yamlBackground": "the background: block",
}

var yamlTypeRE = regexp.MustCompile(`type config\.(\w+)`)

// humanizeYAMLTypes rewrites go-yaml's "field foo not found in type
// config.yamlFile" into the block name the user actually wrote:
// "config.yamlFile" means nothing to whoever typed the key.
func humanizeYAMLTypes(msg string) string {
	return yamlTypeRE.ReplaceAllStringFunc(msg, func(m string) string {
		if label, ok := yamlTypeNames[strings.TrimPrefix(m, "type config.")]; ok {
			return label
		}
		return "the shell3: block"
	})
}

// parseYAML strict-decodes the kit's `shell3:` wiring block, resolves env:
// references from secrets, and fills the wiring fields of c.
func (c *LoadedConfig) parseYAML(data []byte, secrets map[string]string) error {
	var f yamlFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return fmt.Errorf(wiringLabel+": %s", humanizeYAMLTypes(err.Error()))
	}
	if err := resolveEnvRefs(&f, secrets); err != nil {
		return fmt.Errorf(wiringLabel+": %w", err)
	}
	if len(f.Models) == 0 {
		return fmt.Errorf(wiringLabel + ": no models declared")
	}
	for _, name := range slices.Sorted(maps.Keys(f.Models)) {
		m := f.Models[name]
		if m.BaseURL == "" || m.Model == "" {
			return fmt.Errorf(wiringLabel+": model %q needs base_url and model", name)
		}
		// prune_at defaults to compact_at*0.6 so the cheap-prune tier is on by
		// default wherever compaction is; an explicit value at or above
		// compact_at is clamped to 0 (disabled) rather than firing after it.
		// Both tiers key off compact_at at runtime, so an explicit prune_at
		// without compact_at would be silently dead — reject it instead.
		if m.PruneAt != nil && *m.PruneAt > 0 && m.CompactAt <= 0 {
			return fmt.Errorf(wiringLabel+": model %q sets prune_at without compact_at (pruning only runs while compaction is armed)", name)
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
	if tc := f.Telegram; tc != nil {
		c.telegram = TelegramConfig{Present: true, Token: tc.Token, ChatID: tc.ChatID, WorkDir: tc.WorkDir, AllowFrom: tc.AllowFrom}
	}
	for _, name := range slices.Sorted(maps.Keys(f.MCP)) {
		s := f.MCP[name]
		if !mcpNameRE.MatchString(name) {
			return fmt.Errorf(wiringLabel+": mcp server name %q must match %s", name, mcpNameRE)
		}
		if (len(s.Command) == 0) == (s.URL == "") {
			return fmt.Errorf(wiringLabel+": mcp server %q needs exactly one of command or url", name)
		}
		if len(s.Allow) > 0 && len(s.Deny) > 0 {
			return fmt.Errorf(wiringLabel+": mcp server %q: set at most one of allow/deny", name)
		}
		c.mcpServers = append(c.mcpServers, MCPServer{
			Name: name, Command: s.Command, Env: s.Env, URL: s.URL,
			Headers: s.Headers, TimeoutSecs: s.Timeout, Allow: s.Allow, Deny: s.Deny,
		})
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
	// dash_port defaults to 7333 (unset); an explicit 0 disables the dash
	// listener entirely, so — like the keep-days keys — the default can't be
	// a bare int zero value.
	c.DashPort = DefaultDashPort
	if f.DashPort != nil {
		c.DashPort = *f.DashPort
	}
	if c.DashPort < 0 || c.DashPort > 65535 {
		return fmt.Errorf(wiringLabel+": dash_port must be 0 (disabled) or a port 1-65535; got %d", c.DashPort)
	}
	// review_model must name a declared model — a config that loads is a
	// config whose {review} reviewer can actually run. "" = main model.
	c.ReviewModel = strings.TrimSpace(f.ReviewModel)
	c.ReviewPolicy = strings.TrimSpace(f.ReviewPolicy)
	if c.ReviewModel != "" {
		if _, ok := c.Model(c.ReviewModel); !ok {
			return fmt.Errorf(wiringLabel+": review_model %q names no declared model", c.ReviewModel)
		}
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
		return fmt.Errorf(wiringLabel+": %s must not be negative (use 0 to keep forever); got %d", key, days)
	}
	if days > maxKeepDays {
		return fmt.Errorf(wiringLabel+": %s is too large (max %d days); got %d", key, maxKeepDays, days)
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

// DefaultDashPort is where the web dash listens when `dash_port` is unset.
// High and unassigned; an explicit 0 disables the dash entirely.
const DefaultDashPort = 7333
