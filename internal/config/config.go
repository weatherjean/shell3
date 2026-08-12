// Package config loads the shell3 config directory: shell3.yaml (wiring) +
// agent.md / agents/*.md (prompts with frontmatter) + skills/ + cron/ +
// per-agent hooks/*.sh. Prose lives in markdown files, wiring
// lives in YAML, presence of a file enables its feature, and policy is a bash
// hook script — there is no embedded config language.
package config

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
	Bash, BashBg, Edit, Media, Read, List, History bool
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
	// Context is the main agent's `context:` frontmatter: config-dir-relative
	// paths (globs allowed) whose contents are read AT SESSION CREATION and
	// appended to the system prompt under `## Context`. Raw entries only —
	// resolution is late-bound (see ResolveContextFiles / BuildPersonaFor) so a
	// fresh turn always sees the current file. Main agent only; a subagent
	// declaring it is a load error.
	Context []string
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
	// ProjectsBrief is the contents of projects.md beside shell3.yaml (""
	// when absent): the standing Chain of Command portfolio brief, appended
	// verbatim to the end of the main agent's system prompt. Subagents never
	// carry one.
	ProjectsBrief string
}

// Subagent is a delegatable specialist (one agents/*.md): a non-interactive
// agent the model can spawn as an in-process background job via the task
// tool. Description is the model-facing "when to use". No Subagents field:
// delegation is single-level by construction.
type Subagent struct {
	AgentCommon
	Description string
	// Workdir is where the manager's shell runs — set only for project
	// managers (a projects/<name>/ manager.md, where it is the real repo the
	// manager works in, elsewhere on disk). Empty for ordinary subagents,
	// which inherit the host's working directory.
	Workdir string
}

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
}

// STTConfig is the `media.stt:` block: speech-to-text for inbound voice
// notes. Echo controls whether the transcript is echoed back to the user
// before the model turn runs.
type STTConfig struct {
	ModelRef, Language string
	Echo               bool
}

// TTSConfig is the `media.tts:` block: text-to-speech for outbound replies.
// Mode governs when synthesis runs ("off", "inbound", "always"); Format is
// the output codec.
type TTSConfig struct{ ModelRef, Voice, Mode, Format string }

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

	// RunsKeepDays is `runs_keep_days`: how long the janitor keeps a stored
	// session (by its `last_at`) before sweeping it at `shell3 telegram`
	// startup. Always populated at load — default 30; an explicit 0 means
	// keep forever (the sweep is skipped entirely).
	RunsKeepDays int

	// MediaKeepDays is `media_keep_days`: how long the janitor keeps a file
	// under the media dir (attachments, generated images, TTS cache) before
	// sweeping it at `shell3 telegram` startup. Always populated at load —
	// default 0 = keep forever: delivered files and attachments are user
	// data, so deletion is opt-in rather than assumed.
	MediaKeepDays int

	agent     Agent
	subagents []Subagent
	projects  []Project

	mcpServers []MCPServer
	telegram   TelegramConfig
	cron       []CronJob

	stt *STTConfig
	tts *TTSConfig

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

// Projects returns the loaded Chain of Command projects in directory order.
func (c *LoadedConfig) Projects() []Project {
	out := make([]Project, len(c.projects))
	copy(out, c.projects)
	return out
}

// SubagentByName returns the subagent for name — declared by agents/<name>.md
// or a project manager (projects/<name>/manager.md).
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

// Cron returns the parsed cron/ jobs in filename order.
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

// STT returns the parsed media.stt block, nil when not declared.
func (c *LoadedConfig) STT() *STTConfig { return c.stt }

// TTS returns the parsed media.tts block, nil when not declared.
func (c *LoadedConfig) TTS() *TTSConfig { return c.tts }

// MCPServers returns the declared MCP servers sorted by name (YAML map order
// is unspecified; sorting keeps connect order and status listings
// deterministic across loads).
func (c *LoadedConfig) MCPServers() []MCPServer {
	out := make([]MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}
