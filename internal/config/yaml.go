package config

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/weatherjean/shell3/internal/kit"
	"gopkg.in/yaml.v3"
)

// yamlFile is the wire schema of the kit's shell3: block, decoded strictly —
// an unknown key anywhere is a load error.
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

// yamlTelegram is the telegram: block — bot credentials and where the shell
// runs. Token resolves from .env through an env:KEY reference.
type yamlTelegram struct {
	Token     string   `yaml:"token"`
	ChatID    string   `yaml:"chat_id"`
	WorkDir   string   `yaml:"workdir"`
	AllowFrom []string `yaml:"allow_from"`
	// MaxConcurrentTurns caps concurrent chats: rooms are independent, so
	// without it N rooms speaking fan out N agents on one provider account.
	MaxConcurrentTurns int `yaml:"max_concurrent_turns"`
	// Chats tunes individual rooms. Declaring one neither authorizes nor
	// enrols it — that happens when an allowlisted person speaks there.
	Chats []yamlChat `yaml:"chats"`
}

// yamlChat is one chats: entry. A room with no entry takes the defaults,
// which is the normal case.
type yamlChat struct {
	ID string `yaml:"id"`
	// A pointer so unset, meaning the default ON, is distinguishable from an
	// explicit false, which suppresses the description brief.
	UseDescription *bool    `yaml:"use_description"`
	Context        []string `yaml:"context"`
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

// wiringLabel prefixes every wiring error. What is being decoded is the kit's
// shell3: block, so naming a file the operator does not have would send them
// looking for the wrong thing.
const wiringLabel = kit.FileName + " shell3: block"

// yamlTypeNames maps the wire structs onto the wiring blocks they decode, so a
// strict-decode failure reads as config rather than as Go.
var yamlTypeNames = map[string]string{
	"yamlFile":       "the shell3: block",
	"yamlModel":      "a models: entry",
	"yamlTelegram":   "the telegram: block",
	"yamlChat":       "a telegram.chats: entry",
	"yamlMCP":        "an mcp: server",
	"yamlBackground": "the background: block",
}

var yamlTypeRE = regexp.MustCompile(`type config\.(\w+)`)

// humanizeYAMLTypes rewrites go-yaml's "not found in type config.yamlFile"
// into the block name the user actually wrote.
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
		for _, field := range []struct {
			name  string
			value int
		}{
			{"context_window", m.ContextWindow},
			{"compact_at", m.CompactAt},
			{"keep_recent", m.KeepRecent},
			{"max_tokens", m.MaxTokens},
		} {
			if field.value < 0 {
				return fmt.Errorf(wiringLabel+": model %q %s must not be negative; got %d", name, field.name, field.value)
			}
		}
		if m.PruneAt != nil && *m.PruneAt < 0 {
			return fmt.Errorf(wiringLabel+": model %q prune_at must not be negative; got %d", name, *m.PruneAt)
		}
		if m.ContextWindow > 0 && m.CompactAt > m.ContextWindow {
			return fmt.Errorf(wiringLabel+": model %q compact_at (%d) exceeds context_window (%d)", name, m.CompactAt, m.ContextWindow)
		}
		// Defaults to compact_at*0.6, so the cheap tier is on wherever
		// compaction is; at or above compact_at it clamps to 0 rather than
		// firing after it. Both tiers key off compact_at, so a prune_at
		// without one would be silently dead — rejected instead.
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
		// At or above compact_at the verbatim tail exceeds the trigger, so
		// compaction could never get back under it and would re-fire every
		// turn. Clamp to half.
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
		if tc.MaxConcurrentTurns < 0 {
			return fmt.Errorf(wiringLabel+": telegram.max_concurrent_turns must not be negative; got %d", tc.MaxConcurrentTurns)
		}
		chats := make([]ChatConfig, 0, len(tc.Chats))
		seenChat := map[string]bool{}
		for _, ch := range tc.Chats {
			id := strings.TrimSpace(ch.ID)
			if id == "" {
				return fmt.Errorf(wiringLabel + ": telegram.chats entry has no id")
			}
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				// A chats: entry keyed on something that is not a chat id can
				// never match a room, so it would silently do nothing.
				return fmt.Errorf(wiringLabel+": telegram.chats id %q is not a number", id)
			}
			if seenChat[id] {
				return fmt.Errorf(wiringLabel+": telegram.chats id %q is declared more than once", id)
			}
			seenChat[id] = true
			chats = append(chats, ChatConfig{ID: id, UseDescription: ch.UseDescription, Context: ch.Context})
		}
		c.telegram = TelegramConfig{Present: true, Token: tc.Token, ChatID: tc.ChatID, WorkDir: tc.WorkDir,
			AllowFrom: tc.AllowFrom, MaxConcurrentTurns: tc.MaxConcurrentTurns, Chats: chats}
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
		if s.Timeout < 0 {
			return fmt.Errorf(wiringLabel+": mcp server %q timeout must not be negative; got %d", name, s.Timeout)
		}
		c.mcpServers = append(c.mcpServers, MCPServer{
			Name: name, Command: s.Command, Env: s.Env, URL: s.URL,
			Headers: s.Headers, TimeoutSecs: s.Timeout, Allow: s.Allow, Deny: s.Deny,
		})
	}
	if b := f.Background; b != nil {
		if b.MaxConcurrent < 0 {
			return fmt.Errorf(wiringLabel+": background.max_concurrent must not be negative; got %d", b.MaxConcurrent)
		}
		c.BackgroundMaxConcurrent = b.MaxConcurrent
	}
	// Defaults to 30; an explicit 0 means keep forever, so the default cannot
	// be a bare int zero value.
	c.RunsKeepDays = 30
	if f.RunsKeepDays != nil {
		c.RunsKeepDays = *f.RunsKeepDays
	}
	if err := validateKeepDays("runs_keep_days", c.RunsKeepDays); err != nil {
		return err
	}
	// Defaults to 0, keep forever: uploads are user data, so deletion is
	// opt-in.
	c.MediaKeepDays = 0
	if f.MediaKeepDays != nil {
		c.MediaKeepDays = *f.MediaKeepDays
	}
	if err := validateKeepDays("media_keep_days", c.MediaKeepDays); err != nil {
		return err
	}
	// Defaults to 7333; an explicit 0 disables the listener, so as with the
	// keep-days keys the default cannot be a bare zero value.
	c.DashPort = DefaultDashPort
	if f.DashPort != nil {
		c.DashPort = *f.DashPort
	}
	if c.DashPort < 0 || c.DashPort > 65535 {
		return fmt.Errorf(wiringLabel+": dash_port must be 0 (disabled) or a port 1-65535; got %d", c.DashPort)
	}
	// Must name a declared model, so a config that loads is one whose
	// reviewer can run. "" = the main model.
	c.ReviewModel = strings.TrimSpace(f.ReviewModel)
	c.ReviewPolicy = strings.TrimSpace(f.ReviewPolicy)
	if c.ReviewModel != "" {
		if _, ok := c.Model(c.ReviewModel); !ok {
			return fmt.Errorf(wiringLabel+": review_model %q names no declared model", c.ReviewModel)
		}
	}
	return nil
}

// maxKeepDays bounds the keep-days keys at load. Both feed
// `time.Duration(days) * 24 * time.Hour`, which overflows int64 nanoseconds
// past ~106751 days and can wrap to a small POSITIVE duration — inverting
// "keep forever" into "delete almost everything" on the next janitor run.
// 100 years is nowhere near that and already absurd, so anything above it is
// a fat-finger rather than intent.
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
