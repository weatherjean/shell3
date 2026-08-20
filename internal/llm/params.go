package llm

// RequestParams is the set of tunable knobs every adapter understands.
// Empty string / nil = "use adapter default". Adapters must clamp values
// they cannot represent (e.g. the openai adapter clamps xhigh reasoning
// effort down to high).
type RequestParams struct {
	ReasoningEffort   string   // none|minimal|low|medium|high|xhigh
	ParallelToolCalls *bool    // nil = leave provider default
	Temperature       *float64 // nil = leave provider default
	MaxTokens         int      // 0 = adapter default
}

// Merge returns base with non-zero fields from override applied.
func (p RequestParams) Merge(o RequestParams) RequestParams {
	out := p
	if o.ReasoningEffort != "" {
		out.ReasoningEffort = o.ReasoningEffort
	}
	if o.ParallelToolCalls != nil {
		out.ParallelToolCalls = o.ParallelToolCalls
	}
	if o.Temperature != nil {
		out.Temperature = o.Temperature
	}
	if o.MaxTokens > 0 {
		out.MaxTokens = o.MaxTokens
	}
	return out
}
