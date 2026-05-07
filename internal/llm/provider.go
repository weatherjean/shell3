package llm

import (
	"context"
	"fmt"
	"sync"

	"github.com/weatherjean/shell3/internal/config"
)

// Streamer is the streaming surface every LLM client exposes.
type Streamer interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

// ModelSetter is implemented by Streamers that can swap their target
// model in place.
type ModelSetter interface {
	SetModel(model string)
}

// TrafficInspector is implemented by Streamers that buffer the last raw
// HTTP request/response they handled.
type TrafficInspector interface {
	LastTraffic() (req, res []byte)
}

// ReasoningInspector is implemented by Streamers that side-channel
// "reasoning" text out of band of the standard delta stream.
type ReasoningInspector interface {
	LastReasoning() string
}

// Provider is a self-registering LLM backend. Each adapter package
// (internal/adapter/<name>) owns one Provider impl, registers it via
// Register from init(), and is wired in via blank import.
type Provider interface {
	Name() string
	SingleInstance() bool
	NewClient(ctx context.Context, store *config.AuthStore, instance, model string) (Streamer, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register adds a Provider under name. Panics on duplicate registration.
func Register(name string, p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("llm: provider %q already registered", name))
	}
	registry[name] = p
}

// Get returns the Provider registered under name.
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
