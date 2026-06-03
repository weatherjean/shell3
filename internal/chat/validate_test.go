package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateToolArgs(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd":    map[string]any{"type": "string"},
			"reason": map[string]any{"type": "string"},
		},
		"required": []any{"cmd"},
	}

	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{"valid with required", `{"cmd":"ls"}`, false},
		{"valid with extra fields", `{"cmd":"ls","reason":"test"}`, false},
		{"missing required", `{"reason":"test"}`, true},
		{"empty object", `{}`, true},
		{"nil args treated as empty object", ``, true},
		{"not an object", `["array"]`, true},
		{"invalid json", `{bad}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolArgs(schema, json.RawMessage(tc.args))
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateToolArgsNoRequired(t *testing.T) {
	schema := map[string]any{"type": "object"}
	// No required list — any object passes.
	if err := validateToolArgs(schema, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := validateToolArgs(schema, json.RawMessage(`{"anything":"goes"}`)); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateToolArgsEmptyRequired(t *testing.T) {
	schema := map[string]any{"required": []any{}}
	if err := validateToolArgs(schema, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty required should pass: %v", err)
	}
}

func TestValidateToolArgsMultipleMissing(t *testing.T) {
	schema := map[string]any{
		"required": []any{"a", "b", "c"},
	}
	err := validateToolArgs(schema, json.RawMessage(`{"a":1}`))
	if err == nil {
		t.Fatal("expected error for missing b and c")
	}
	msg := err.Error()
	if !containsAllSubstrings(msg, "b", "c") {
		t.Fatalf("error should mention all missing fields: %s", msg)
	}
}

func containsAllSubstrings(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
