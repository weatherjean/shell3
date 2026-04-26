package usertools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAll_ParsesSpec(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: hello
description: Say hello
enabled: true
parameters:
  type: object
  properties:
    who:
      type: string
  required: [who]
command: 'echo "hi $WHO"'
`
	if err := os.WriteFile(filepath.Join(dir, "hello.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	tools, warnings, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Spec.Name != "hello" {
		t.Errorf("name: got %q", tools[0].Spec.Name)
	}
	if tools[0].Spec.Command != `echo "hi $WHO"` {
		t.Errorf("command: got %q", tools[0].Spec.Command)
	}
	if !tools[0].Spec.Enabled {
		t.Error("expected enabled")
	}
}

func TestValidate(t *testing.T) {
	avail := map[string]struct{}{"FOO_KEY": {}}
	objParams := map[string]any{"type": "object", "properties": map[string]any{}}
	cases := []struct {
		name    string
		s       Spec
		wantErr string
	}{
		{"missing name", Spec{Description: "d", Command: "c", Parameters: objParams}, "name: required"},
		{"bad name", Spec{Name: "Bad-Name", Description: "d", Command: "c", Parameters: objParams}, "must match"},
		{"reserved name", Spec{Name: "bash", Description: "d", Command: "c", Parameters: objParams}, "reserved"},
		{"missing desc", Spec{Name: "ok", Command: "c", Parameters: objParams}, "description: required"},
		{"missing cmd", Spec{Name: "ok", Description: "d", Parameters: objParams}, "command: required"},
		{"missing params", Spec{Name: "ok", Description: "d", Command: "c"}, "parameters: required"},
		{"params not object", Spec{Name: "ok", Description: "d", Command: "c", Parameters: map[string]any{"type": "string"}}, "must be \"object\""},
		{"secret missing", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams, Secrets: []string{"NOPE"}}, "not set"},
		{"ok with secret", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams, Secrets: []string{"FOO_KEY"}}, ""},
		{"ok no secret", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.s, avail)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoadAll_SkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: off_tool
description: d
enabled: false
parameters: {type: object, properties: {}}
command: 'echo'
`
	os.WriteFile(filepath.Join(dir, "off.yaml"), []byte(yaml), 0644)

	tools, _, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestLoadAll_ProjectOverridesGlobal(t *testing.T) {
	global := t.TempDir()
	project := t.TempDir()
	g := `name: hello
description: from global
enabled: true
parameters: {type: object, properties: {}}
command: 'echo global'
`
	p := `name: hello
description: from project
enabled: true
parameters: {type: object, properties: {}}
command: 'echo project'
`
	os.WriteFile(filepath.Join(global, "hello.yaml"), []byte(g), 0644)
	os.WriteFile(filepath.Join(project, "hello.yaml"), []byte(p), 0644)

	tools, _, err := LoadAll([]string{global, project}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Description != "from project" {
		t.Fatalf("project should win: %+v", tools)
	}
}

func TestLoadAll_InvalidYieldsWarning(t *testing.T) {
	dir := t.TempDir()
	bad := `name: BAD
description: d
enabled: true
parameters: {type: object, properties: {}}
command: 'echo'
`
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(bad), 0644)

	tools, warnings, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatal("expected no tools")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "must match") {
		t.Fatalf("expected name warning, got %v", warnings)
	}
}
