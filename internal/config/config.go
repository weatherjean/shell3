// Package config loads the shell3 config directory: shell3.yaml (wiring) +
// agent.md / agents/*.md (prompts with frontmatter) + skills/ + cron/ +
// heartbeat.md + per-agent hooks/*.sh. Prose lives in markdown files, wiring
// lives in YAML, presence of a file enables its feature, and policy is a bash
// hook script — there is no embedded config language.
package config

import "time"

// Model is one declared model under shell3.yaml `models:`.
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
	Bash, BashBg, Edit, Media bool
}

// Skill is one resolved *.md from the skills/ dir, surfaced as a one-line
// entry in the ## Skills index; the agent reads the body at Path (absolute)
// with `cat` when the skill applies.
type Skill struct{ Name, Description, Path string }

// AgentCommon holds the fields agents and subagents share: the frontmatter of
// an agent markdown file plus its body (the prompt).
type AgentCommon struct {
	Name, ModelName, Prompt string
	Gates                   ToolGates
	// Skills is the scan of the config dir's skills/ (main agent only;
	// subagents carry none).
	Skills []Skill
	// MCP is the `mcp:` frontmatter opt-in: the shell3.yaml mcp server names
	// whose tools this agent gets. MCPAll is the `mcp: all` form. Both
	// empty/false (the default) means no MCP tools.
	MCP    []string
	MCPAll bool
	// Prune toggles the cheap tool-output-stubbing tier for this agent.
	// nil (unset) inherits the model's prune_at; false skips pruning entirely.
	Prune *bool
}

// Agent is the main agent (agent.md). Subagents lists every registered
// subagent name (delegation is inferred: non-empty agents/ = task tool on).
type Agent struct {
	AgentCommon
	Subagents []string
}

// Subagent is a delegatable specialist (one agents/*.md): a non-interactive
// agent the model can spawn as an in-process background job via the task
// tool. Description is the model-facing "when to use". No Subagents field:
// delegation is single-level by construction.
type Subagent struct {
	AgentCommon
	Description string
}

// TelegramConfig is the parsed `telegram:` block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig is the `telegram.dashboard:` block. Tunnel, if set, is a
// shell command spawned at bot start ({addr} replaced by Addr) whose output is
// scanned for the dashboard's public https URL; URL, if set, is the fixed
// public address and wins over a scanned one.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
	Tunnel  string
}

// WebConfig is the parsed `web:` block — the standalone web front-end
// (shell3 web): the dashboard plus chat, served over plain HTTP with token
// auth instead of Telegram initData.
type WebConfig struct {
	Addr   string
	Secret string
	URL    string
	Tunnel string
}

// CronJob is one parsed cron/<name>.md job.
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// Heartbeat is the parsed heartbeat.md: a periodic check-in turn injected
// into the main session while it is idle. Checklist is the standing orders
// (the file body); the model replies HEARTBEAT_OK when nothing needs
// attention and the host suppresses the message.
type Heartbeat struct {
	Every     time.Duration
	Checklist string
	Prompt    string // preamble override; "" = built-in default
	// ActiveFrom/ActiveTo bound ticking to a daily window ("HH:MM", from
	// inclusive, to exclusive; from > to spans midnight). Both empty = 24/7.
	ActiveFrom string
	ActiveTo   string
	TZ         string // IANA zone for the window; "" = host-local
}

// STTConfig is the `media.stt:` block: speech-to-text for inbound voice
// messages. Echo controls whether the transcript is echoed back to the user
// before the model turn runs.
type STTConfig struct {
	ModelRef, Language string
	Echo               bool
}

// TTSConfig is the `media.tts:` block: text-to-speech for outbound replies.
// Mode governs when synthesis runs ("off", "inbound", "always"); Format is
// the output codec.
type TTSConfig struct{ ModelRef, Voice, Mode, Format string }

// DescribeConfig is the `media.describe:` block: captions an inbound image
// before the model turn runs.
type DescribeConfig struct{ ModelRef, Prompt string }

// ImagegenConfig is the `media.imagegen:` block: image generation. API
// selects the wire shape ("openai" or "openrouter").
type ImagegenConfig struct{ ModelRef, Size, API string }

// MCPServer is one declared server from the shell3.yaml `mcp:` block.
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

	agent     Agent
	subagents []Subagent

	mcpServers []MCPServer
	telegram   TelegramConfig
	web        WebConfig
	cron       []CronJob
	heartbeat  *Heartbeat

	stt      *STTConfig
	tts      *TTSConfig
	describe *DescribeConfig
	imagegen *ImagegenConfig

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

// Close releases config resources. The declarative config holds none; the
// method exists so the loader's lifecycle matches what front-ends expect.
func (c *LoadedConfig) Close() {}

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

// Agents returns the main agent as a one-element slice (there is exactly one
// agent.md; the slice shape keeps list-style call sites simple).
func (c *LoadedConfig) Agents() []Agent { return []Agent{c.agent} }

// AgentByName returns the main agent when name matches it.
func (c *LoadedConfig) AgentByName(name string) (Agent, bool) {
	if c.agent.Name == name {
		return c.agent, true
	}
	return Agent{}, false
}

// FirstAgent returns the main agent (the default when a caller doesn't name
// one). Load guarantees it exists.
func (c *LoadedConfig) FirstAgent() Agent { return c.agent }

// Subagents returns a copy of the registered subagents in filename order.
func (c *LoadedConfig) Subagents() []Subagent {
	out := make([]Subagent, len(c.subagents))
	copy(out, c.subagents)
	return out
}

// SubagentByName returns the subagent declared by agents/<name>.md.
func (c *LoadedConfig) SubagentByName(name string) (Subagent, bool) {
	for _, s := range c.subagents {
		if s.Name == name {
			return s, true
		}
	}
	return Subagent{}, false
}

// Telegram returns the parsed `telegram:` block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }

// Web returns the parsed `web:` block (zero value if absent).
func (c *LoadedConfig) Web() WebConfig { return c.web }

// Cron returns the parsed cron/ jobs in filename order.
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

// Heartbeat returns the parsed heartbeat.md, nil when absent.
func (c *LoadedConfig) Heartbeat() *Heartbeat { return c.heartbeat }

// STT returns the parsed media.stt block, nil when not declared.
func (c *LoadedConfig) STT() *STTConfig { return c.stt }

// TTS returns the parsed media.tts block, nil when not declared.
func (c *LoadedConfig) TTS() *TTSConfig { return c.tts }

// Describe returns the parsed media.describe block, nil when not declared.
func (c *LoadedConfig) Describe() *DescribeConfig { return c.describe }

// Imagegen returns the parsed media.imagegen block, nil when not declared.
func (c *LoadedConfig) Imagegen() *ImagegenConfig { return c.imagegen }

// MCPServers returns the declared MCP servers sorted by name (YAML map order
// is unspecified; sorting keeps connect order and status listings
// deterministic across loads).
func (c *LoadedConfig) MCPServers() []MCPServer {
	out := make([]MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}
