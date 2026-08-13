package kit

import "sort"

// jsonType maps a declared param type onto its JSON Schema name. It is also the
// allowlist: a type absent here is a load error.
var jsonType = map[string]string{
	"string": "string",
	"int":    "integer",
	"bool":   "boolean",
}

// Schema renders the tool's params as the JSON Schema object the model sees.
func (t Tool) Schema() map[string]any {
	props := map[string]any{}
	var required []string
	for name, p := range t.Params {
		prop := map[string]any{"type": jsonType[p.Type]}
		if p.Desc != "" {
			prop["description"] = p.Desc
		}
		if p.Default != nil {
			prop["default"] = p.Default
		}
		props[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)

	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
