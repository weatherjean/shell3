// Package config loads the shell3 config directory. Its centre is the kit,
// shell3.sh (parsed by internal/kit): this package lifts the kit's `shell3:`
// wiring block through the strict YAML parser, reads .env and cron/*.md, and
// owns the tool schemas, the skill scan, the `context:` resolver, and the
// gate/note execution the kit declares. Presence of a file enables its
// feature, and policy is a bash function — there is no embedded config
// language.
package config

// Model is one declared model under the kit wiring's `models:`.
type Model struct {
	Name, BaseURL, APIKey, ModelID string
	ContextWindow                  int
	// CompactAt is the absolute prompt-token threshold at which the host
	// auto-compacts conversation history before the next turn. 0 (unset)
	// disables auto-compaction. See chat.maybeCompact.
	CompactAt int
	// KeepRecent is the verbatim tail (in prompt tokens) preserved across an
	// auto-compaction. 0 (unset) derives a default from CompactAt. See
	// chat.resolveKeepRecent.
	KeepRecent int
	// PruneAt is the lower threshold; stub old tool outputs with no LLM call.
	// 0 disables. Must be below CompactAt (clamped to 0 if not).
	PruneAt     int
	Reasoning   string
	MaxTokens   int
	Temperature *float64
	Extra       map[string]any
	// RunProxy, if set, is a shell command spawned (detached, fire-and-forget)
	// the first time an agent activates this model — used to bring up a local
	// proxy/translation shim in front of BaseURL. See internal/modelproxy.
	RunProxy string
}

type ToolGates struct {
	Bash, BashBg, Edit, Media, Read, List, History bool
}

// Skill is one resolved *.md from the skills/ dir, surfaced as a one-line
// entry in the ## Skills index; the agent reads the body at Path (absolute)
// with `cat` when the skill applies.
type Skill struct{ Name, Description, Path string }

// TelegramConfig is the parsed `telegram:` block: the bot's credentials and
// where the agent's shell runs.
type TelegramConfig struct {
	// Present reports whether a telegram: block was declared at all. It tells
	// "no front-end configured" (legitimate for an `shell3 ask`-only config)
	// apart from "front-end declared but unusable" — the second is a config
	// error worth failing `shell3 health` over, the first is not.
	Present bool
	// Token is the bot API token, secret-substituted from .env. Empty means a
	// declared block left it blank (Present is the absence signal): `shell3
	// boot` writes exactly that when the user defers the token, so the load
	// succeeds and the front-end refuses to start instead.
	Token  string
	ChatID string
	// AllowFrom lists the Telegram user ids permitted to drive the agent.
	// Authorization is per SENDER, not per chat: chat_id says WHERE the bot
	// talks, this says WHO it obeys, and in a group those are different
	// questions — every member can see the chat, but membership must not
	// confer an unrestricted shell.
	//
	// Empty means "the chat_id owner only", which is exactly the historical
	// single-DM behaviour (in a DM the chat id IS the user id), so an existing
	// config keeps working without naming anyone.
	AllowFrom []string
	WorkDir   string
}

// CronJob is one parsed cron/<name>.md job.
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	// Direct posts the run's raw result straight to the user, skipping the
	// default agent-mail turn. The cost valve: a default cron tick wakes the
	// main model to judge its result; a direct one costs no tokens at all.
	Direct bool
	// Tool names a kit tool this job runs directly — no agent, no model turn.
	// Exactly one of Agent or Tool is set. A tool job is the valve for
	// mechanical, idempotent work (a sync, a rotation): the turn a prompt job
	// spends judging its own output is the whole cost of running it often.
	Tool string
}

// MCPServer is one declared server from the wiring's `mcp:` block.
// Exactly one of Command (stdio) or URL (streamable HTTP) is set — enforced
// at load.
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
	// BackgroundMaxConcurrent is the maximum number of concurrent background
	// jobs (`background.max_concurrent`). 0 means unset; the runtime applies
	// the default (8) at the read site.
	BackgroundMaxConcurrent int

	// RunsKeepDays is `runs_keep_days`: how long the janitor keeps a stored
	// session (by its `last_at`) before sweeping it at `shell3 telegram`
	// startup. Always populated at load — default 30; an explicit 0 means
	// keep forever (the sweep is skipped entirely).
	RunsKeepDays int

	// MediaKeepDays is `media_keep_days`: how long the janitor keeps a file
	// under the media dir (attachments) before sweeping it at `shell3
	// telegram` startup. Always populated at load —
	// default 0 = keep forever: delivered files and attachments are user
	// data, so deletion is opt-in rather than assumed.
	MediaKeepDays int

	// DashPort is `dash_port`: where the read-only web dash listens on
	// 127.0.0.1. Always populated at load — default 7333; an explicit 0
	// disables the listener (and /dash says so).
	DashPort int

	// ReviewModel is `review_model`: the declared model the gate's {review}
	// reviewer runs on. "" = use the main agent's model. Validated at load
	// against the models map.
	ReviewModel string
	// ReviewPolicy is `review_policy`: operator rule text appended to the
	// reviewer's system prompt (the trusted channel). "" = none.
	ReviewPolicy string

	// kitMainAgent is the kit's first agent when a kit installed its gates
	// (SetKitHooks). It keys to "" in the hook maps — see hookKey.
	kitMainAgent string

	mcpServers []MCPServer
	telegram   TelegramConfig
	cron       []CronJob

	// hooks maps each governed agent to its hook scripts (see hooks.go).
	hooks hookSet

	// dir is the absolute config directory this config was loaded from.
	dir string

	// warnings accumulates non-fatal config issues found at load time (e.g. a
	// skipped invalid skill file, or an orphan hook file). The caller drains
	// them via Warnings(); `shell3 health` hardens them into failures.
	warnings []string
}

// Warnings returns the non-fatal issues collected while loading the config.
// Empty on a clean load.
func (c *LoadedConfig) Warnings() []string { return c.warnings }

// Dir returns the absolute config directory this config was loaded from.
func (c *LoadedConfig) Dir() string { return c.dir }

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

// Cron returns the parsed cron/ jobs in filename order.
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

// MCPServers returns the declared MCP servers sorted by name (YAML map order
// is unspecified; sorting keeps connect order and status listings
// deterministic across loads).
func (c *LoadedConfig) MCPServers() []MCPServer {
	out := make([]MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}
