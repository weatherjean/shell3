package kit

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
	declGate
	declNote
	declCommand
	declEvent
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
	case declGate:
		return "gate"
	case declNote:
		return "note"
	case declCommand:
		return "command"
	case declEvent:
		return "event"
	default:
		return "wiring"
	}
}

// nameList is a YAML value that may be written as one name or a list of them.
// Gates, notes and events are NAMED rather than positional — one function
// typically governs several agents — so `gate: main` and
// `gate: [main, assistant]` are both natural and both accepted.
type nameList []string

func (n *nameList) UnmarshalYAML(value *yaml.Node) error {
	var one string
	if err := value.Decode(&one); err == nil {
		*n = nameList{one}
		return nil
	}
	var many []string
	if err := value.Decode(&many); err != nil {
		return fmt.Errorf("expected an agent name or a list of them")
	}
	*n = many
	return nil
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
	context []string
	mcp     []string
	mcpAll  bool
	params  map[string]Param
	wiring  map[string]any
	agents  []string // declGate/declNote/declEvent: the agents this block governs
	on      []string // declEvent: the event kinds this subscriber receives
}

// blockYAML is the strict shape every declaration block decodes into. Exactly
// one kind key may be set (the `shell3:` header block sets none).
type blockYAML struct {
	Agent  string   `yaml:"agent"`
	Shared string   `yaml:"shared"`
	Tool   string   `yaml:"tool"`
	Skill  string   `yaml:"skill"`
	Test   string   `yaml:"test"`
	Gate   nameList `yaml:"gate"`
	Note   nameList `yaml:"note"`
	Event  nameList `yaml:"event"`

	// Command is install-wide rather than per-agent, so it names ITSELF the
	// way tool:/skill: do, not the agents it governs the way gate: does.
	Command string `yaml:"command"`

	Description string           `yaml:"description"`
	Model       string           `yaml:"model"`
	Workdir     string           `yaml:"workdir"`
	Use         []string         `yaml:"use"`
	Context     []string         `yaml:"context"`
	MCP         nameList         `yaml:"mcp"`
	Params      map[string]Param `yaml:"params"`
	On          []string         `yaml:"on"`

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
		workdir: y.Workdir, use: y.Use, context: y.Context, params: y.Params, wiring: y.Shell3,
		mcp: y.MCP, mcpAll: len(y.MCP) == 1 && y.MCP[0] == "all", on: y.On,
	}

	kinds := []struct {
		k declKind
		n string
	}{
		{declAgent, y.Agent}, {declShared, y.Shared},
		{declTool, y.Tool}, {declSkill, y.Skill}, {declTest, y.Test},
		{declCommand, y.Command},
	}
	found := 0
	for _, c := range kinds {
		if c.n != "" {
			d.kind, d.name = c.k, c.n
			found++
		}
	}
	for _, c := range []struct {
		k declKind
		l nameList
	}{{declGate, y.Gate}, {declNote, y.Note}, {declEvent, y.Event}} {
		if len(c.l) > 0 {
			d.kind, d.agents, d.name = c.k, c.l, strings.Join(c.l, ", ")
			found++
		}
	}
	switch {
	case found > 1:
		return decl{}, fmt.Errorf("line %d: declaration block names more than one of agent/shared/tool/skill/test/command/gate/note/event", b.line)
	case found == 1:
		return d, nil
	case y.Shell3 != nil:
		d.kind = declWiring
		return d, nil
	default:
		return decl{}, fmt.Errorf("line %d: declaration block names no agent/shared/tool/skill/test/command/gate/note/event", b.line)
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
