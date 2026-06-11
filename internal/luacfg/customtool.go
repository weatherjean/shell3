package luacfg

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ResolvedCall is a custom-tool invocation reduced to what the executor (in
// internal/chat) needs: the bash command, the environment to run it with
// (declared params + secrets, as KEY=VALUE), and the dispatch knobs.
type ResolvedCall struct {
	Command    string
	Env        []string
	Background bool
	Timeout    int
}

// ResolveCustomCall validates a custom-tool call and returns its ResolvedCall.
// Only arguments matching a DECLARED parameter are exported (so a misbehaving
// model cannot inject arbitrary env vars). Each declared secret is looked up in
// .env and exported by name; a missing secret is an error (never a silent
// empty value). The command itself is the trusted, author-defined template — it
// is NOT passed through wrap_bash (the model supplies only env values).
func (c *LoadedConfig) ResolveCustomCall(name, argsJSON string) (ResolvedCall, error) {
	tool, ok := c.Tools[name]
	if !ok {
		return ResolvedCall{}, fmt.Errorf("unknown custom tool %q", name)
	}
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ResolvedCall{}, fmt.Errorf("tool %q: bad args json: %w", name, err)
		}
	}
	declared := declaredParamNames(tool.Parameters)
	env := make([]string, 0, len(args)+len(tool.Secrets))
	for k, v := range args {
		if !declared[k] {
			continue // undeclared key: never export (anti-injection)
		}
		env = append(env, k+"="+envValue(v))
	}
	for _, s := range tool.Secrets {
		val, ok := c.Secrets[s]
		if !ok {
			return ResolvedCall{}, fmt.Errorf("tool %q: secret %q not found in .env", name, s)
		}
		env = append(env, s+"="+val)
	}
	return ResolvedCall{Command: tool.Command, Env: env, Background: tool.Background, Timeout: tool.Timeout}, nil
}

// declaredParamNames returns the set of property names from a tool's JSON-schema
// parameters map.
func declaredParamNames(params map[string]any) map[string]bool {
	out := map[string]bool{}
	if props, ok := params["properties"].(map[string]any); ok {
		for k := range props {
			out[k] = true
		}
	}
	return out
}

// envValue renders a JSON-decoded argument as an environment value: scalars in
// their natural string form (numbers without trailing zeros), composites as
// compact JSON.
func envValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
