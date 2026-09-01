// Package config loads the shell3 config directory, whose centre is the kit
// shell3.sh (parsed by internal/kit). It lifts the kit's `shell3:` wiring
// block through the strict YAML parser, reads .env, and owns the tool
// schemas, the skill scan, the `context:` resolver, and gate/note execution.
// Policy is a bash function; there is no embedded config language.
package config

import "github.com/weatherjean/shell3/internal/kit"

const (
	// GroupMessagesAddressed accepts only /commands, @mentions, and replies to
	// the bot in groups. It is the default.
	GroupMessagesAddressed = "addressed"
	// GroupMessagesAll accepts every group message from an allowlisted sender.
	GroupMessagesAll = "all"
)

// Model is one declared model under the kit wiring's `models:`.
type Model struct {
	Name, BaseURL, APIKey, ModelID string
	ContextWindow                  int
	// CompactAt is the prompt-token threshold for auto-compaction before the
	// next turn; 0 disables it. See chat.maybeCompact.
	CompactAt int
	// KeepRecent is the verbatim tail preserved across an auto-compaction, in
	// prompt tokens; 0 derives it from CompactAt (chat.resolveKeepRecent).
	KeepRecent int
	// PruneAt is the lower threshold; stub old tool outputs with no LLM call.
	// 0 disables. Must be below CompactAt (clamped to 0 if not).
	PruneAt     int
	Reasoning   string
	MaxTokens   int
	Temperature *float64
	Extra       map[string]any
	// RunProxy is spawned detached the first time an agent activates this
	// model, to bring up a local shim in front of BaseURL (internal/modelproxy).
	RunProxy string
}

// Skill is one *.md from skills/, surfaced as a one-line entry in the
// ## Skills index; the agent `cat`s the body at Path when it applies.
type Skill struct{ Name, Description, Path string }

// TelegramConfig is the parsed `telegram:` block: the bot's credentials and
// where the agent's shell runs.
type TelegramConfig struct {
	// Present separates "no front-end configured" (legitimate for an
	// ask-only config) from "declared but unusable" — only the second fails
	// `shell3 health`.
	Present bool
	// Token is the bot API token from .env. Empty means a declared block left
	// it blank — what `shell3 boot` writes when the token is deferred, so the
	// load succeeds and the front-end refuses to start instead.
	Token  string
	ChatID string
	// AllowFrom is who may drive the agent. Authorization is per SENDER:
	// chat_id says WHERE the bot talks, this says WHO it obeys — in a group
	// every member sees the chat, and membership must not confer a shell.
	// Empty means the chat_id owner only (in a DM the chat id is the user id).
	AllowFrom []string
	WorkDir   string
	// GroupMessages is "addressed" (default) or "all". Sender authorization
	// applies before either mode, so "all" never means all group members.
	GroupMessages string
	// MaxConcurrentTurns bounds turns across all chats; 0 = the default.
	MaxConcurrentTurns int
	// Chats is per-room tuning. Not an allowlist and not an enrolment list —
	// a room becomes known when an allowlisted person speaks in it.
	Chats []ChatConfig
}

// ChatConfig is one room's declared configuration from `telegram.chats`.
type ChatConfig struct {
	ID string
	// UseDescription feeds the group description into the room's brief.
	// nil = on; false is the escape hatch for a room whose admins are not
	// trusted to write standing context.
	UseDescription *bool
	// Context appends files, resolved like the agent's own `context:`, to the
	// room's brief — the trusted channel for a real project brief.
	Context []string
}

// MCPServer is one server from the wiring's `mcp:` block. Exactly one of
// Command (stdio) or URL (streamable HTTP) is set, enforced at load.
type MCPServer struct {
	Name        string
	Command     []string          // stdio child argv; empty when URL is set
	Env         map[string]string // extra environment for the stdio child
	URL         string            // streamable HTTP endpoint
	Headers     map[string]string // extra HTTP headers (e.g. Authorization)
	TimeoutSecs int               // connect+list and per-call timeout; 0 = default
	Allow, Deny []string          // tool-name filters; at most one list may be set
}

// LoadedConfig is the parsed config directory.
type LoadedConfig struct {
	Models  []Model
	Secrets map[string]string
	// BackgroundMaxConcurrent caps concurrent background jobs; 0 = unset, and
	// the runtime applies its default (8) at the read site.
	BackgroundMaxConcurrent int

	// RunsKeepDays is how long the startup janitor keeps a session, by its
	// last_at. Always populated; default 30, an explicit 0 = keep forever.
	RunsKeepDays int

	// MediaKeepDays is the same for the media dir. Default 0 = keep forever:
	// attachments are user data, so deletion is opt-in.
	MediaKeepDays int

	// ReviewModel is the model the gate's {review} reviewer runs on, "" = the
	// main agent's. Validated at load against the models map.
	ReviewModel string
	// ReviewPolicy is operator rule text for the reviewer's system prompt.
	ReviewPolicy string

	// kitMainAgent is the kit's first agent when a kit installed its gates
	// (SetKitHooks). It keys to "" in the hook maps — see hookKey.
	kitMainAgent string

	mcpServers []MCPServer
	telegram   TelegramConfig

	// hooks maps each governed agent to its hook scripts (see hooks.go).
	hooks hookSet

	// eventOn holds each event subscriber's `on:` kind filter, keyed the same
	// way hooks[hookEvent] is. Kept beside rather than inside hookRef because
	// only this one kind has a filter.
	eventOn map[string][]string

	// dir is the absolute config directory this config was loaded from.
	dir string

	// kit is the parsed shell3.sh the wiring above was lifted from. Kept so
	// agents, tools, skills and cron jobs come from the SAME parse the wiring
	// did, rather than every consumer re-reading and re-parsing the file.
	kit *kit.Kit

	// warnings are non-fatal load issues (a skipped invalid skill file, …),
	// drained via Warnings(); `shell3 health` hardens them into failures.
	warnings []string
}

// Warnings returns the non-fatal issues collected while loading the config.
// Empty on a clean load.
func (c *LoadedConfig) Warnings() []string { return c.warnings }

// Dir returns the absolute config directory this config was loaded from.
func (c *LoadedConfig) Dir() string { return c.dir }

// Kit returns the parsed kit this config was loaded from (never nil after a
// successful Load — a directory without a kit does not load).
func (c *LoadedConfig) Kit() *kit.Kit { return c.kit }

func (c *LoadedConfig) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// HasMCPServer reports whether the wiring declares an mcp server by that name.
func (c *LoadedConfig) HasMCPServer(name string) bool {
	for _, s := range c.mcpServers {
		if s.Name == name {
			return true
		}
	}
	return false
}

// Telegram returns the parsed `telegram:` block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }

// MCPServers copies the declared servers. Parse sorts them by name (YAML map
// order is unspecified), keeping connect order and listings deterministic.
func (c *LoadedConfig) MCPServers() []MCPServer {
	out := make([]MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}
