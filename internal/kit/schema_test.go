package kit

import (
	"reflect"
	"testing"
)

func TestToolSchema(t *testing.T) {
	tool := Tool{
		Name: "stack-check",
		Desc: "Classify a site's stack",
		Params: map[string]Param{
			"url":     {Type: "string", Required: true, Desc: "homepage URL"},
			"timeout": {Type: "int", Default: 20},
		},
	}

	got := tool.Schema()
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":     map[string]any{"type": "string", "description": "homepage URL"},
			"timeout": map[string]any{"type": "integer", "default": 20},
		},
		"required": []string{"url"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema =\n%#v\nwant\n%#v", got, want)
	}
}

func TestToolSchemaNoParams(t *testing.T) {
	got := Tool{Name: "ping", Desc: "ping"}.Schema()
	props, ok := got["properties"].(map[string]any)
	if !ok || len(props) != 0 {
		t.Fatalf("properties = %#v, want empty map", got["properties"])
	}
	if _, has := got["required"]; has {
		t.Fatalf("required should be absent when nothing is required: %#v", got)
	}
}

func TestToolSchemaSortsRequired(t *testing.T) {
	tool := Tool{Params: map[string]Param{
		"zebra": {Type: "string", Required: true},
		"apple": {Type: "string", Required: true},
	}}
	req, _ := tool.Schema()["required"].([]string)
	if len(req) != 2 || req[0] != "apple" || req[1] != "zebra" {
		t.Fatalf("required = %v, want sorted [apple zebra]", req)
	}
}
