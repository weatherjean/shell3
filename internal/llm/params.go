package llm

import "fmt"

// RequestParams is the set of tunable knobs every adapter understands.
// Empty string / nil = "use adapter default". Adapters must clamp values
// they cannot represent (e.g. anthropic mapping reasoning_effort → budget).
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

// SetByName mutates the field corresponding to a /parameters command name.
// Adapter-specific validation runs separately via ParamSpec.Validate.
func (p *RequestParams) SetByName(name, value string) error {
	switch name {
	case "reasoning_effort":
		p.ReasoningEffort = value
	case "parallel_tool_calls":
		b := value == "true"
		p.ParallelToolCalls = &b
	case "temperature":
		var f float64
		if _, err := fmt.Sscanf(value, "%f", &f); err != nil {
			return fmt.Errorf("temperature: %w", err)
		}
		p.Temperature = &f
	case "max_tokens":
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
			return fmt.Errorf("max_tokens: %w", err)
		}
		p.MaxTokens = n
	default:
		return fmt.Errorf("unknown parameter %q", name)
	}
	return nil
}

// ParamSpec describes one parameter an adapter understands.
// Empty Enum = freeform value. Default is informational (used by /parameters list).
type ParamSpec struct {
	Name    string
	Enum    []string
	Default string
}

// Validate returns nil if value is acceptable for this spec.
func (s ParamSpec) Validate(value string) error {
	if len(s.Enum) == 0 {
		return nil
	}
	for _, v := range s.Enum {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("%s: %q not in %v", s.Name, value, s.Enum)
}

// ParamSetter is implemented by Streamers that accept runtime parameter
// overrides. Defaults come from adapter construction; SetParams replaces them.
type ParamSetter interface {
	SetParams(p RequestParams)
}

// ParamDescriber is implemented by Streamers that expose their tunable
// parameter surface for /parameters list and validation.
type ParamDescriber interface {
	ParamSpecs() []ParamSpec
}
