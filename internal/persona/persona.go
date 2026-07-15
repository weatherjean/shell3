// Package persona holds the slim runtime carrier for an agent's system
// prompt, tool schema, and request parameters. Loading and templating live in
// internal/luacfg; this package is just the data structure that the chat
// session and front-ends read from.
package persona

import "github.com/weatherjean/shell3/internal/llm"

// Persona holds a ready-to-use agent: system prompt and exposed tool schema.
type Persona struct {
	Name         string
	SystemPrompt string
	Tools        []llm.ToolDefinition
}
