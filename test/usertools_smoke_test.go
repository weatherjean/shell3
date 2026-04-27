package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
	"github.com/weatherjean/shell3/internal/usertools"
)

func TestUserTools_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, ".shell3", "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `name: greet
description: Say hi
enabled: true
parameters:
  type: object
  properties:
    who: {type: string}
  required: [who]
command: 'echo "hello $WHO"'
`
	if err := os.WriteFile(filepath.Join(toolsDir, "greet.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	secStore, err := secrets.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := secStore.Set("IGNORED", "v"); err != nil {
		t.Fatal(err)
	}
	envMap := secStore.All()
	avail := map[string]struct{}{}
	for k := range envMap {
		avail[k] = struct{}{}
	}
	tools, warnings, err := usertools.LoadAll([]string{toolsDir}, avail)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("expected one greet tool, got %+v", tools)
	}

	out, err := usertools.Run(context.Background(), tools[0], `{"who":"world"}`, envMap, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("unexpected output: %q", out)
	}
}
