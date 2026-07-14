// Package persona holds the slim runtime carrier for an agent's system
// prompt, tool schema, and request parameters. Loading and templating live in
// internal/luacfg; this package is just the data structure that the chat
// session and front-ends read from.
package persona

import "github.com/weatherjean/shell3/internal/llm"

// ToolDef is an alias so callers don't import llm directly.
type ToolDef = llm.ToolDefinition

// Persona holds a ready-to-use agent: system prompt, exposed tool schema, and
// provider request parameters.
type Persona struct {
	Name         string
	SystemPrompt string
	Tools        []llm.ToolDefinition
	Parameters   llm.RequestParams
}
