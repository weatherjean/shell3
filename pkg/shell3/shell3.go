package shell3

import (
	"context"
	"fmt"
	"os"

	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/pkg/hooks"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// Options configures a slim bootstrap for library embedding.
// All fields are optional except where noted.
type Options struct {
	// Ctx is used when constructing the LLM client. Defaults to
	// context.Background() when nil.
	Ctx context.Context

	// HomeDir is the user home directory. Defaults to os.UserHomeDir() when empty.
	HomeDir string

	// Provider is the auth-store instance name (e.g. "openai", "anthropic").
	// When empty, the first configured instance wins.
	Provider string

	// Model is the model id. When empty, the first model in the provider's
	// models list wins.
	Model string

	// SystemPrompt overrides the base persona's system prompt. Empty uses
	// the built-in baseline.
	SystemPrompt string

	// Tools is the schema list shown to the model. Empty means no tools.
	// Embedders that want bash/edit/etc. can pass chat tool definitions or
	// build their own.
	Tools []llm.ToolDefinition

	// WorkDir is the working directory passed to tool handlers. Defaults to
	// os.Getwd when empty.
	WorkDir string

	// Headless when true marks the run as subprocess/embed.
	Headless bool
}

// New returns a slim chat.Config suitable for embedding. Performs the
// minimum bootstrap: reads auth, constructs the LLM adapter, builds a
// base persona. Does NOT touch the filesystem beyond reading the auth
// file and (optionally) the user-supplied WorkDir. Does NOT create
// ~/.shell3/projects/, .shell3/ in cwd, log files, or SQLite stores.
//
// Returned cleanup is a no-op today but is reserved for future
// resources (e.g. log file handles if Headless logging is added).
func New(opts Options) (chat.Config, func(), error) {
	noop := func() {}

	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get home directory: %w", err)
		}
		homeDir = h
	}

	workDir := opts.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}

	authStore, err := config.LoadAuthStore(homeDir)
	if err != nil {
		return chat.Config{}, noop, fmt.Errorf("load auth: %w", err)
	}

	instances := authStore.List()
	if len(instances) == 0 {
		return chat.Config{}, noop, fmt.Errorf("no provider configured — run 'shell3 auth'")
	}

	instanceName := opts.Provider
	if instanceName == "" {
		instanceName = instances[0].Name
	}

	inst, ok := authStore.Get(instanceName)
	if !ok {
		return chat.Config{}, noop, fmt.Errorf("provider %q not in auth store", instanceName)
	}

	model := opts.Model
	if model == "" {
		if len(inst.Models) == 0 {
			return chat.Config{}, noop, fmt.Errorf("provider %q has no models configured", instanceName)
		}
		model = inst.Models[0].ID
	}

	provider, ok := llm.Get(inst.Type)
	if !ok {
		return chat.Config{}, noop, fmt.Errorf("unknown adapter type %q for instance %q — set type to \"openai\" or \"anthropic\"", inst.Type, instanceName)
	}
	streamer, err := provider.NewClient(ctx, authStore, instanceName, model)
	if err != nil {
		return chat.Config{}, noop, fmt.Errorf("build adapter: %w", err)
	}

	p := persona.BasePersona(opts.SystemPrompt, opts.Tools)

	cfg := chat.Config{
		LLM:         streamer,
		Hooks:       hooks.NewRunner(hooks.Config{}),
		Personality: p,
		WorkDir:     workDir,
		StatusLine:  instanceName + " │ " + model,
		ModeLabel:   "base",
		Headless:    opts.Headless,
		Log:         applog.Noop{},
		Params:      llm.RequestParams{},
	}

	return cfg, noop, nil
}
