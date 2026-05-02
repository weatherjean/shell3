// Package usertools loads and runs user-defined tool specs from
// .shell3/tools/*.yaml files.
package usertools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/persona"
)

// Spec is the on-disk YAML format for a user tool.
type Spec struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Enabled     bool           `yaml:"enabled"`
	Parameters  map[string]any `yaml:"parameters"`
	Command     string         `yaml:"command"`
	Secrets     []string       `yaml:"secrets,omitempty"`
	Timeout     time.Duration  `yaml:"timeout,omitempty"`
	Cwd         string         `yaml:"cwd,omitempty"`
	Before      string         `yaml:"before,omitempty"`
	After       string         `yaml:"after,omitempty"`
}

// Tool is a loaded, validated user tool with its source path attached.
type Tool struct {
	Spec
	Path string
}

// reservedNames is derived from the actual built-in tool registry.
var reservedNames = persona.BuiltinToolNames()

// LoadAll walks each dir in order and returns enabled, validated tools.
// Later dirs override earlier ones on name collision (project beats global).
// availableSecrets is the set of keys present in the project secrets
// store; tools that declare missing secrets are disabled with a warning.
func LoadAll(dirs []string, availableSecrets map[string]struct{}) (tools []Tool, warnings []string, err error) {
	byName := map[string]Tool{}
	for _, dir := range dirs {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, warnings, fmt.Errorf("usertools: read dir %s: %w", dir, readErr)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			path := filepath.Join(dir, name)
			data, rdErr := os.ReadFile(path)
			if rdErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: read: %v", path, rdErr))
				continue
			}
			var s Spec
			if uErr := yaml.Unmarshal(data, &s); uErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: parse: %v", path, uErr))
				continue
			}
			if !s.Enabled {
				continue
			}
			if vErr := Validate(s, availableSecrets); vErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, vErr))
				continue
			}
			byName[s.Name] = Tool{Spec: s, Path: path}
		}
	}
	for _, t := range byName {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, warnings, nil
}

// Validate checks a Spec for problems. Returns nil when usable.
func Validate(s Spec, availableSecrets map[string]struct{}) error {
	if s.Name == "" {
		return fmt.Errorf("name: required")
	}
	if !validName(s.Name) {
		return fmt.Errorf("name %q: must match [a-z][a-z0-9_]*", s.Name)
	}
	if _, reserved := reservedNames[s.Name]; reserved {
		return fmt.Errorf("name %q: reserved by built-in tool", s.Name)
	}
	if s.Description == "" {
		return fmt.Errorf("description: required")
	}
	if s.Command == "" {
		return fmt.Errorf("command: required")
	}
	if s.Parameters == nil {
		return fmt.Errorf("parameters: required (use {type: object, properties: {}} for no args)")
	}
	if t, _ := s.Parameters["type"].(string); t != "object" {
		return fmt.Errorf("parameters.type: must be \"object\"")
	}
	for _, sec := range s.Secrets {
		if _, ok := availableSecrets[sec]; !ok {
			return fmt.Errorf("secret %q: not set; run `shell3 secrets set --key %s --secret VALUE`", sec, sec)
		}
	}
	return nil
}

func validName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
