package persona

// BasePersona returns a minimal persona: no skills, no user tools, no hooks —
// just a short system prompt and the caller-provided built-in tools (which may
// also be empty). Production personas are assembled from Lua config
// (internal/luacfg); this constructor exists for tests that need a bare
// persona without a config load.
func BasePersona(systemPrompt string, tools []ToolDef) Persona {
	if systemPrompt == "" {
		systemPrompt = baseSystemPrompt
	}
	return Persona{
		Name:         "base",
		SystemPrompt: systemPrompt,
		Tools:        tools,
	}
}

const baseSystemPrompt = `You are an LLM running inside the shell3 harness.

Respond directly to user queries. Tools are only available if your config has explicitly enabled them — if none are listed in your tool schema, do not invent tool calls.`
