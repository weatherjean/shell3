package chat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validateToolArgs checks that args satisfies the required constraints from
// schema. Only the "required" array is enforced — checking that each named
// field is present in the decoded object. Unknown or extra fields are allowed.
// Returns nil when args is valid or when schema carries no "required" list.
func validateToolArgs(schema map[string]any, args json.RawMessage) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		return fmt.Errorf("args must be a JSON object: %w", err)
	}

	req, _ := schema["required"]
	if req == nil {
		return nil
	}

	var required []string
	switch v := req.(type) {
	case []string:
		required = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				required = append(required, s)
			}
		}
	default:
		return nil
	}

	var missing []string
	for _, field := range required {
		if _, ok := obj[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
