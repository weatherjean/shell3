package llm

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Streamer is the streaming surface every LLM client exposes.
// *Client (the OpenAI-compatible client) satisfies this.
type Streamer interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

// Provider is a self-registering model backend that owns its own auth flow.
// API-key providers do not implement this — they go through the existing
// credentials.yaml + NewClient(baseURL, apiKey, model) path.
type Provider interface {
	// Auth runs the provider's interactive authentication (e.g. OAuth) and
	// persists any tokens it needs. Called by `shell3 auth --provider=<name>`.
	Auth(ctx context.Context, w io.Writer) error

	// NewClient constructs a ready-to-use Streamer for the given model.
	// Implementations should refresh stored credentials lazily as needed.
	NewClient(ctx context.Context, model string) (Streamer, error)

	// Models lists the model identifiers this provider exposes.
	Models() []string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register adds a Provider under name. Intended for use from package init().
// Panics on duplicate registration to surface wiring mistakes loudly.
func Register(name string, p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("llm: provider %q already registered", name))
	}
	registry[name] = p
}

// Get returns the Provider registered under name, or false if none.
func Get(name string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Registered returns the names of all registered providers.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}
