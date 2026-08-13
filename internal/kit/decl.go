package kit

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Param is one declared tool parameter. Type is "string", "int", or "bool".
// Params reach the tool's shell function as environment variables.
type Param struct {
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
	Default  any    `yaml:"default"`
	Desc     string `yaml:"description"`
}

type declKind int

const (
	declAgent declKind = iota
	declShared
	declTool
	declSkill
	declTest
	declWiring
)

func (k declKind) String() string {
	switch k {
	case declAgent:
		return "agent"
	case declShared:
		return "shared"
	case declTool:
		return "tool"
	case declSkill:
		return "skill"
	case declTest:
		return "test"
	default:
		return "wiring"
	}
}

// decl is one decoded declaration block. line/endLine are the kit file lines of
// the opening and closing fence; endLine is where function binding starts
// looking.
type decl struct {
	kind    declKind
	name    string
	line    int
	endLine int
	desc    string
	model   string
	workdir string
	use     []string
	params  map[string]Param
	wiring  map[string]any
}

// blockYAML is the strict shape every declaration block decodes into. Exactly
// one kind key may be set (the `shell3:` header block sets none).
type blockYAML struct {
	Agent  string `yaml:"agent"`
	Shared string `yaml:"shared"`
	Tool   string `yaml:"tool"`
	Skill  string `yaml:"skill"`
	Test   string `yaml:"test"`

	Description string           `yaml:"description"`
	Model       string           `yaml:"model"`
	Workdir     string           `yaml:"workdir"`
	Use         []string         `yaml:"use"`
	Params      map[string]Param `yaml:"params"`

	Shell3 map[string]any `yaml:"shell3"`
}

// decodeBlock strict-decodes one block's YAML and classifies it.
func decodeBlock(b block) (decl, error) {
	var y blockYAML
	dec := yaml.NewDecoder(bytes.NewReader(b.yaml))
	dec.KnownFields(true)
	if err := dec.Decode(&y); err != nil {
		return decl{}, fmt.Errorf("line %d: %s", b.line, absLines(err.Error(), b.line))
	}

	d := decl{
		line: b.line, endLine: b.endLine, desc: y.Description, model: y.Model,
		workdir: y.Workdir, use: y.Use, params: y.Params, wiring: y.Shell3,
	}

	kinds := []struct {
		k declKind
		n string
	}{
		{declAgent, y.Agent}, {declShared, y.Shared},
		{declTool, y.Tool}, {declSkill, y.Skill}, {declTest, y.Test},
	}
	found := 0
	for _, c := range kinds {
		if c.n != "" {
			d.kind, d.name = c.k, c.n
			found++
		}
	}
	switch {
	case found > 1:
		return decl{}, fmt.Errorf("line %d: declaration block names more than one of agent/shared/tool/skill/test", b.line)
	case found == 1:
		return d, nil
	case y.Shell3 != nil:
		d.kind = declWiring
		return d, nil
	default:
		return decl{}, fmt.Errorf("line %d: declaration block names no agent/shared/tool/skill/test", b.line)
	}
}

// yamlLineRef matches the "line N:" references yaml.v3 emits, which are
// relative to the block's own text.
var yamlLineRef = regexp.MustCompile(`line (\d+):`)

// absLines rewrites a YAML error's block-relative line numbers into kit file
// line numbers. The block's first content line sits one line below its opening
// fence, so relative line 1 is start+1. Without this an author reading
// "line 6: ... line 2: field not found" has to do the arithmetic themselves.
func absLines(msg string, start int) string {
	return yamlLineRef.ReplaceAllStringFunc(msg, func(m string) string {
		n, err := strconv.Atoi(yamlLineRef.FindStringSubmatch(m)[1])
		if err != nil {
			return m
		}
		return fmt.Sprintf("line %d:", start+n)
	})
}
