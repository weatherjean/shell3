package persona

// BasePersona returns a minimal persona suitable for library embedding.
// No skills, no user tools, no hooks — just a short system prompt and
// the caller-provided built-in tools (which may also be empty).
//
// The system prompt instructs the model that it's running inside the
// shell3 harness as an LLM. Embedders enable tools by passing them via
// the tools parameter or by mutating the returned Persona.
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

Respond directly to user queries. Tools are only available if the embedding application has explicitly enabled them — if none are listed in your tool schema, do not invent tool calls.`
