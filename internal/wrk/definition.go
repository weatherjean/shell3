// Package wrk parses and validates *.wrk.lisp workflow definitions.
package wrk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/sexpr"
)

type Definition struct {
	Path     string
	Name     string
	Root     string
	Parallel int
	Timeout  time.Duration
	Nodes    []Node
}

type NodeKind string

const (
	AgentNode   NodeKind = "agent"
	LoopNode    NodeKind = "loop"
	CommandNode NodeKind = "command"
	WaitNode    NodeKind = "wait"
)

type Node struct {
	Name    string
	Kind    NodeKind
	Using   string
	After   []string
	Access  string
	Max     int
	Timeout time.Duration
	Prompt  string
	Run     string
	Until   *Check
	Accept  *Check
	Event   string
	Message string
	Pos     sexpr.Position
}

type Check struct {
	Kind  string
	Value string
}

func Load(path string, cfg *lispconfig.Config) (*Definition, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read wrkfile: %w", err)
	}
	return Parse(path, src, cfg)
}

func Parse(path string, src []byte, cfg *lispconfig.Config) (*Definition, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wrk: configuration is required")
	}
	forms, err := sexpr.Parse(path, src)
	if err != nil {
		return nil, err
	}
	if len(forms) != 1 {
		return nil, fmt.Errorf("%s: wrkfile must contain exactly one task form", path)
	}
	head, body, ok := forms[0].Form()
	if !ok || head != "task" || len(body) == 0 {
		return nil, wrkAt(forms[0], "root form must be task with a name")
	}
	name, err := text(body[0])
	if err != nil {
		return nil, err
	}
	if !sexpr.ValidName(name) {
		return nil, wrkAt(body[0], "invalid task name %q", name)
	}
	d := &Definition{Path: path, Name: name, Root: ".", Parallel: 1}
	fields := map[string]bool{}
	names := map[string]sexpr.Node{}
	for _, child := range body[1:] {
		kind, args, ok := child.Form()
		if !ok {
			return nil, wrkAt(child, "task entries must be forms")
		}
		switch kind {
		case "root":
			if fields[kind] {
				return nil, wrkAt(child, "duplicate task field %q", kind)
			}
			fields[kind] = true
			d.Root, err = exactlyText(child, args)
		case "parallel":
			if fields[kind] {
				return nil, wrkAt(child, "duplicate task field %q", kind)
			}
			fields[kind] = true
			d.Parallel, err = positiveInt(child, args)
		case "timeout":
			if fields[kind] {
				return nil, wrkAt(child, "duplicate task field %q", kind)
			}
			fields[kind] = true
			d.Timeout, err = duration(child, args)
		case string(AgentNode), string(LoopNode), string(CommandNode), string(WaitNode):
			var n Node
			n, err = parseNode(NodeKind(kind), child, args, cfg)
			if err == nil {
				if previous, exists := names[n.Name]; exists {
					err = wrkAt(child, "duplicate node %q (first declared at %s)", n.Name, previous.Pos)
				} else {
					names[n.Name] = child
					d.Nodes = append(d.Nodes, n)
				}
			}
		default:
			err = wrkAt(child, "unknown task form %q", kind)
		}
		if err != nil {
			return nil, err
		}
	}
	if len(d.Nodes) == 0 {
		return nil, wrkAt(forms[0], "task must declare at least one node")
	}
	if err := validateGraph(d); err != nil {
		return nil, err
	}
	return d, nil
}

func parseNode(kind NodeKind, node sexpr.Node, args []sexpr.Node, cfg *lispconfig.Config) (Node, error) {
	if len(args) == 0 || args[0].Kind != sexpr.Symbol {
		return Node{}, wrkAt(node, "%s requires a symbolic node name", kind)
	}
	n := Node{Name: args[0].Value, Kind: kind, Access: "read", Pos: node.Pos}
	if !sexpr.ValidName(n.Name) {
		return Node{}, wrkAt(args[0], "invalid node name %q", n.Name)
	}
	seen := map[string]bool{}
	for _, field := range args[1:] {
		name, values, ok := field.Form()
		if !ok {
			return Node{}, wrkAt(field, "%s entries must be forms", kind)
		}
		if seen[name] {
			return Node{}, wrkAt(field, "duplicate %s field %q", kind, name)
		}
		seen[name] = true
		var err error
		switch name {
		case "using":
			n.Using, err = exactlySymbol(field, values)
		case "after":
			if len(values) == 0 {
				err = wrkAt(field, "after requires at least one node")
				break
			}
			for _, value := range values {
				if value.Kind != sexpr.Symbol {
					err = wrkAt(value, "dependency names must be symbols")
					break
				}
				n.After = append(n.After, value.Value)
			}
		case "access":
			n.Access, err = enum(field, values, "read", "write")
		case "max":
			n.Max, err = positiveInt(field, values)
		case "timeout":
			n.Timeout, err = duration(field, values)
		case "prompt":
			n.Prompt, err = exactlyText(field, values)
		case "run":
			n.Run, err = exactlyText(field, values)
		case "until":
			n.Until, err = parseCheck(field, values)
		case "accept":
			n.Accept, err = parseCheck(field, values)
		case "for":
			n.Event, err = parseEvent(field, values)
		case "message":
			n.Message, err = exactlyText(field, values)
		default:
			err = wrkAt(field, "unknown %s field %q", kind, name)
		}
		if err != nil {
			return Node{}, err
		}
	}
	if err := validateNode(node, &n, seen, cfg); err != nil {
		return Node{}, err
	}
	return n, nil
}

func validateNode(source sexpr.Node, n *Node, seen map[string]bool, cfg *lispconfig.Config) error {
	allowed := map[NodeKind]map[string]bool{
		AgentNode:   {"using": true, "after": true, "access": true, "timeout": true, "prompt": true, "accept": true},
		LoopNode:    {"using": true, "after": true, "access": true, "max": true, "timeout": true, "prompt": true, "until": true},
		CommandNode: {"after": true, "access": true, "timeout": true, "run": true, "accept": true},
		WaitNode:    {"after": true, "timeout": true, "for": true, "message": true},
	}[n.Kind]
	for field := range seen {
		if !allowed[field] {
			return wrkAt(source, "%s node %q cannot use %q", n.Kind, n.Name, field)
		}
	}
	switch n.Kind {
	case AgentNode:
		if n.Using == "" || n.Prompt == "" {
			return wrkAt(source, "agent %q requires using and prompt", n.Name)
		}
	case LoopNode:
		if n.Using == "" || n.Prompt == "" || n.Until == nil || n.Max == 0 {
			return wrkAt(source, "loop %q requires using, prompt, max, and until", n.Name)
		}
	case CommandNode:
		if n.Run == "" {
			return wrkAt(source, "command %q requires run", n.Name)
		}
	case WaitNode:
		n.Access = "read"
		if n.Event == "" {
			return wrkAt(source, "wait %q requires for", n.Name)
		}
	}
	if n.Using != "" {
		if _, exists := cfg.Agents[n.Using]; !exists {
			return wrkAt(source, "%s %q uses unknown agent %q", n.Kind, n.Name, n.Using)
		}
	}
	return nil
}

func validateGraph(d *Definition) error {
	byName := make(map[string]Node, len(d.Nodes))
	for _, n := range d.Nodes {
		byName[n.Name] = n
	}
	for _, n := range d.Nodes {
		seen := map[string]bool{}
		for _, dep := range n.After {
			if seen[dep] {
				return fmt.Errorf("%s: node %q repeats dependency %q", n.Pos, n.Name, dep)
			}
			seen[dep] = true
			if dep == n.Name {
				return fmt.Errorf("%s: node %q depends on itself", n.Pos, n.Name)
			}
			if _, exists := byName[dep]; !exists {
				return fmt.Errorf("%s: node %q depends on unknown node %q", n.Pos, n.Name, dep)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("%s: dependency cycle reaches node %q", byName[name].Pos, name)
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, dep := range byName[name].After {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, name)
		visited[name] = true
		return nil
	}
	for _, n := range d.Nodes {
		if err := visit(n.Name); err != nil {
			return err
		}
	}
	return nil
}

func parseCheck(node sexpr.Node, args []sexpr.Node) (*Check, error) {
	if len(args) != 1 {
		return nil, wrkAt(node, "check requires one (sh TEXT) or (file PATH) form")
	}
	kind, values, ok := args[0].Form()
	if !ok || (kind != "sh" && kind != "file") {
		return nil, wrkAt(args[0], "check must be (sh TEXT) or (file PATH)")
	}
	value, err := exactlyText(args[0], values)
	if err != nil {
		return nil, err
	}
	if kind == "file" && !safeArtifactPath(value) {
		return nil, wrkAt(args[0], "artifact path must be relative and stay inside the artifact directory")
	}
	return &Check{Kind: kind, Value: value}, nil
}

func safeArtifactPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func parseEvent(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 {
		return "", wrkAt(node, "for requires one (event NAME) form")
	}
	head, values, ok := args[0].Form()
	if !ok || head != "event" || len(values) != 1 {
		return "", wrkAt(args[0], "for requires one (event NAME) form")
	}
	return text(values[0])
}

func exactlySymbol(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 || args[0].Kind != sexpr.Symbol {
		return "", wrkAt(node, "form requires one symbol")
	}
	return args[0].Value, nil
}

func exactlyText(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 {
		return "", wrkAt(node, "form requires one string or symbol")
	}
	return text(args[0])
}

func text(node sexpr.Node) (string, error) {
	if node.Kind == sexpr.String || node.Kind == sexpr.Symbol {
		return node.Value, nil
	}
	return "", wrkAt(node, "value must be a string or symbol")
}

func positiveInt(node sexpr.Node, args []sexpr.Node) (int, error) {
	if len(args) != 1 || args[0].Kind != sexpr.Number || args[0].Integer <= 0 || args[0].Integer > int64(^uint(0)>>1) {
		return 0, wrkAt(node, "value must be a positive integer")
	}
	return int(args[0].Integer), nil
}

func duration(node sexpr.Node, args []sexpr.Node) (time.Duration, error) {
	value, err := exactlyText(node, args)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, wrkAt(node, "value must be a positive duration")
	}
	return d, nil
}

func enum(node sexpr.Node, args []sexpr.Node, allowed ...string) (string, error) {
	value, err := exactlySymbol(node, args)
	if err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", wrkAt(node, "value %q must be one of %v", value, allowed)
}

func wrkAt(node sexpr.Node, format string, args ...any) error {
	return fmt.Errorf("%s: %s", node.Pos, fmt.Sprintf(format, args...))
}
