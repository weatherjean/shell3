// Package lispconfig loads the new inert shell3.lisp configuration.
package lispconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/internal/sexpr"
)

// Config is one resolved shell3.lisp kit.
type Config struct {
	Memory    string
	Models    map[string]Model
	Main      *Orchestrator
	Skills    []Skill
	Runners   map[string]Runner
	Agents    map[string]Agent
	Schedules []Schedule
	Telegram  *Telegram
}

// Model is one OpenAI-compatible endpoint used by the attached orchestrator
// turn. APIKeyEnv names the inherited process variable whose value is resolved
// only when the runtime is built or reloaded.
type Model struct {
	BaseURL       string
	APIKeyEnv     string
	ID            string
	Reasoning     string
	MaxTokens     int
	ContextWindow int
}

type Orchestrator struct {
	Model  string
	Prompt string
}

type Skill struct {
	Name         string
	Description  string
	Instructions string
}

// Telegram is the optional remote-control adapter. TokenEnv names a secret;
// the parser never resolves or stores the bot token itself.
type Telegram struct {
	TokenEnv           string
	HomeChat           int64
	AllowFrom          []string
	MaxConcurrentTurns int
	GroupMessages      string
}

// Schedule is one calendar trigger for a durable wrkfile invocation. Calendar
// ownership belongs to exactly one persistent shell3 process; the workflow
// remains the sole place where executable work is declared.
type Schedule struct {
	Name     string
	Cron     string
	Timezone string
	Wrkfile  string
	Request  string
	Output   string
	Timeout  time.Duration
	Overlap  string
	Notify   string
}

type Runner struct {
	Command     []Arg
	Arguments   []Argument
	Stderr      string
	Result      string
	SuccessExit int
	Timeout     time.Duration
}

type Arg struct {
	Literal string
	Slot    string
}

type Argument struct {
	Args []Arg
	When string
}

type parameter struct {
	Required bool
	Default  *string
}

type Agent struct {
	Runner     string
	Parameters map[string]string
}

var runtimeSlots = map[string]bool{
	"agent-name": true, "prompt-file": true, "result-file": true,
	"task-artifacts": true, "task-attempt": true, "task-id": true,
	"task-root": true, "task-run": true, "workdir": true,
}

func Load(path string) (*Config, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(path, src)
}

func Parse(path string, src []byte) (*Config, error) {
	forms, err := sexpr.Parse(path, src)
	if err != nil {
		return nil, err
	}
	if len(forms) != 1 {
		return nil, fmt.Errorf("%s: config must contain exactly one shell3 form", path)
	}
	head, body, ok := forms[0].Form()
	if !ok || head != "shell3" {
		return nil, at(forms[0], "root form must be shell3")
	}
	constants := map[string]string{}
	c := &Config{Models: map[string]Model{}, Runners: map[string]Runner{}, Agents: map[string]Agent{}}

	// Constants are deliberately order-independent, so runner declarations can
	// refer to a definition placed later in the file.
	for _, child := range body {
		name, args, ok := child.Form()
		if !ok {
			return nil, at(child, "shell3 entries must be forms")
		}
		if name != "define" {
			continue
		}
		if len(args) != 2 || args[0].Kind != sexpr.Symbol {
			return nil, at(child, "define requires a symbol and one literal value")
		}
		key := args[0].Value
		if !sexpr.ValidName(key) {
			return nil, at(args[0], "invalid constant name %q", key)
		}
		if _, exists := constants[key]; exists {
			return nil, at(args[0], "duplicate constant %q", key)
		}
		value, err := literal(args[1])
		if err != nil {
			return nil, err
		}
		constants[key] = value
	}

	var rawAgents []sexpr.Node
	runnerParameters := map[string]map[string]parameter{}
	var rawMain *sexpr.Node
	var rawTelegram *sexpr.Node
	seenMemory := false
	seenSkills := map[string]bool{}
	seenSchedules := map[string]bool{}
	seenVersion := false
	for _, child := range body {
		name, args, _ := child.Form()
		switch name {
		case "version":
			if seenVersion {
				return nil, at(child, "duplicate version")
			}
			if len(args) != 1 || args[0].Kind != sexpr.Number || args[0].Integer != 1 {
				return nil, at(child, "version must be 1")
			}
			seenVersion = true
		case "define":
			// Resolved in the first pass.
		case "memory":
			if seenMemory {
				return nil, at(child, "duplicate memory")
			}
			value, err := oneLiteral(child, args)
			if err != nil {
				return nil, err
			}
			c.Memory, seenMemory = value, true
		case "model":
			modelName, m, err := parseModel(child, args)
			if err != nil {
				return nil, err
			}
			if _, exists := c.Models[modelName]; exists {
				return nil, at(child, "duplicate model %q", modelName)
			}
			c.Models[modelName] = m
		case "orchestrator":
			if rawMain != nil {
				return nil, at(child, "duplicate orchestrator")
			}
			n := child
			rawMain = &n
		case "skill":
			skill, err := parseSkill(child, args)
			if err != nil {
				return nil, err
			}
			if seenSkills[skill.Name] {
				return nil, at(child, "duplicate skill %q", skill.Name)
			}
			seenSkills[skill.Name] = true
			c.Skills = append(c.Skills, skill)
		case "telegram":
			if rawTelegram != nil {
				return nil, at(child, "duplicate telegram")
			}
			n := child
			rawTelegram = &n
		case "schedule":
			schedule, err := parseSchedule(child, args)
			if err != nil {
				return nil, err
			}
			if seenSchedules[schedule.Name] {
				return nil, at(child, "duplicate schedule %q", schedule.Name)
			}
			seenSchedules[schedule.Name] = true
			c.Schedules = append(c.Schedules, schedule)
		case "runner":
			runnerName, r, parameters, err := parseRunner(child, args, constants)
			if err != nil {
				return nil, err
			}
			if _, exists := c.Runners[runnerName]; exists {
				return nil, at(child, "duplicate runner %q", runnerName)
			}
			c.Runners[runnerName] = r
			runnerParameters[runnerName] = parameters
		case "agent":
			rawAgents = append(rawAgents, child)
		default:
			return nil, at(child, "unknown shell3 form %q", name)
		}
	}
	if !seenVersion {
		return nil, at(forms[0], "missing (version 1)")
	}
	if rawMain != nil {
		_, args, _ := rawMain.Form()
		main, err := parseOrchestrator(*rawMain, args, c.Models)
		if err != nil {
			return nil, err
		}
		c.Main = &main
	}
	if rawTelegram != nil {
		_, args, _ := rawTelegram.Form()
		telegram, err := parseTelegram(*rawTelegram, args)
		if err != nil {
			return nil, err
		}
		c.Telegram = &telegram
	}
	for _, node := range rawAgents {
		_, args, _ := node.Form()
		agentName, a, err := parseAgent(node, args, c.Runners, runnerParameters)
		if err != nil {
			return nil, err
		}
		if _, exists := c.Agents[agentName]; exists {
			return nil, at(node, "duplicate agent %q", agentName)
		}
		c.Agents[agentName] = a
	}
	return c, nil
}

func parseSchedule(node sexpr.Node, args []sexpr.Node) (Schedule, error) {
	if len(args) < 1 || args[0].Kind != sexpr.Symbol || !sexpr.ValidName(args[0].Value) {
		return Schedule{}, at(node, "schedule requires a valid symbolic name")
	}
	s := Schedule{Name: args[0].Value, Overlap: "skip", Notify: "main"}
	seen := map[string]bool{}
	for _, field := range args[1:] {
		name, values, ok := field.Form()
		if !ok {
			return Schedule{}, at(field, "schedule entries must be forms")
		}
		if seen[name] {
			return Schedule{}, at(field, "duplicate schedule field %q", name)
		}
		seen[name] = true
		var err error
		switch name {
		case "cron":
			s.Cron, err = oneLiteral(field, values)
		case "timezone":
			s.Timezone, err = oneLiteral(field, values)
		case "run":
			if len(values) != 1 {
				err = at(field, "run requires (wrkfile PATH)")
				break
			}
			head, nested, ok := values[0].Form()
			if !ok || head != "wrkfile" {
				err = at(field, "run requires (wrkfile PATH)")
				break
			}
			s.Wrkfile, err = oneLiteral(values[0], nested)
		case "request":
			s.Request, err = oneLiteral(field, values)
		case "output":
			s.Output, err = oneLiteral(field, values)
		case "timeout":
			var raw string
			raw, err = oneLiteral(field, values)
			if err == nil {
				s.Timeout, err = time.ParseDuration(raw)
				if err != nil || s.Timeout <= 0 {
					err = at(field, "timeout must be a positive duration")
				}
			}
		case "overlap":
			s.Overlap, err = oneOf(field, values, "skip", "allow")
		case "notify":
			s.Notify, err = oneLiteral(field, values)
		default:
			err = at(field, "unknown schedule field %q", name)
		}
		if err != nil {
			return Schedule{}, err
		}
	}
	if strings.TrimSpace(s.Cron) == "" {
		return Schedule{}, at(node, "schedule %q is missing cron", s.Name)
	}
	if strings.TrimSpace(s.Timezone) == "" {
		return Schedule{}, at(node, "schedule %q is missing timezone", s.Name)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return Schedule{}, at(node, "schedule %q has invalid timezone %q", s.Name, s.Timezone)
	}
	if _, err := robcron.ParseStandard("CRON_TZ=" + s.Timezone + " " + s.Cron); err != nil {
		return Schedule{}, at(node, "schedule %q has invalid cron %q: %v", s.Name, s.Cron, err)
	}
	if s.Wrkfile == "" {
		return Schedule{}, at(node, "schedule %q is missing run", s.Name)
	}
	if !strings.HasSuffix(s.Wrkfile, ".wrk.lisp") {
		return Schedule{}, at(node, "schedule %q wrkfile must end in .wrk.lisp", s.Name)
	}
	cleanWrkfile := filepath.Clean(s.Wrkfile)
	if filepath.IsAbs(s.Wrkfile) || cleanWrkfile == "." || cleanWrkfile == ".." || strings.HasPrefix(cleanWrkfile, ".."+string(filepath.Separator)) {
		return Schedule{}, at(node, "schedule %q wrkfile must be relative to the config directory", s.Name)
	}
	s.Wrkfile = cleanWrkfile
	if s.Timeout <= 0 {
		return Schedule{}, at(node, "schedule %q is missing timeout", s.Name)
	}
	if strings.TrimSpace(s.Output) == "" {
		return Schedule{}, at(node, "schedule %q is missing output", s.Name)
	}
	cleanOutput := filepath.Clean(s.Output)
	if filepath.IsAbs(s.Output) || cleanOutput == "." || cleanOutput == ".." || strings.HasPrefix(cleanOutput, ".."+string(filepath.Separator)) {
		return Schedule{}, at(node, "schedule %q output must be relative to the workflow artifacts directory", s.Name)
	}
	s.Output = cleanOutput
	if len(s.Request) > 1<<20 {
		return Schedule{}, at(node, "schedule %q request exceeds %d bytes", s.Name, 1<<20)
	}
	if strings.TrimSpace(s.Notify) == "" {
		return Schedule{}, at(node, "schedule %q notify destination is empty", s.Name)
	}
	return s, nil
}

func parseTelegram(node sexpr.Node, args []sexpr.Node) (Telegram, error) {
	t := Telegram{MaxConcurrentTurns: 4, GroupMessages: "addressed"}
	seen := map[string]bool{}
	for _, field := range args {
		name, values, ok := field.Form()
		if !ok {
			return Telegram{}, at(field, "telegram entries must be forms")
		}
		if seen[name] {
			return Telegram{}, at(field, "duplicate telegram field %q", name)
		}
		seen[name] = true
		switch name {
		case "token-env":
			value, err := oneSymbol(field, values)
			if err != nil {
				return Telegram{}, err
			}
			t.TokenEnv = value
		case "home-chat":
			if len(values) != 1 || values[0].Kind != sexpr.Number || values[0].Integer == 0 {
				return Telegram{}, at(field, "home-chat must be a non-zero integer")
			}
			t.HomeChat = values[0].Integer
		case "allow-from":
			if len(values) == 0 {
				return Telegram{}, at(field, "allow-from requires at least one positive user id")
			}
			seenIDs := map[int64]bool{}
			for _, value := range values {
				if value.Kind != sexpr.Number || value.Integer <= 0 {
					return Telegram{}, at(value, "allow-from values must be positive user ids")
				}
				if seenIDs[value.Integer] {
					return Telegram{}, at(value, "duplicate allow-from user id %d", value.Integer)
				}
				seenIDs[value.Integer] = true
				t.AllowFrom = append(t.AllowFrom, strconv.FormatInt(value.Integer, 10))
			}
		case "max-concurrent-turns":
			if len(values) != 1 || values[0].Kind != sexpr.Number || values[0].Integer <= 0 {
				return Telegram{}, at(field, "max-concurrent-turns must be a positive integer")
			}
			t.MaxConcurrentTurns = int(values[0].Integer)
		case "group-messages":
			value, err := oneOf(field, values, "addressed", "all")
			if err != nil {
				return Telegram{}, err
			}
			t.GroupMessages = value
		default:
			return Telegram{}, at(field, "unknown telegram field %q", name)
		}
	}
	if t.TokenEnv == "" {
		return Telegram{}, at(node, "telegram is missing token-env")
	}
	if t.HomeChat == 0 {
		return Telegram{}, at(node, "telegram is missing home-chat")
	}
	if t.HomeChat < 0 && len(t.AllowFrom) == 0 {
		return Telegram{}, at(node, "telegram home-chat is a group; allow-from must name an operator")
	}
	return t, nil
}

func parseModel(node sexpr.Node, args []sexpr.Node) (string, Model, error) {
	if len(args) < 1 || args[0].Kind != sexpr.Symbol || !sexpr.ValidName(args[0].Value) {
		return "", Model{}, at(node, "model requires a valid symbolic name")
	}
	name := args[0].Value
	m := Model{Reasoning: "medium", MaxTokens: 16000}
	seen := map[string]bool{}
	for _, field := range args[1:] {
		name, values, ok := field.Form()
		if !ok {
			return "", Model{}, at(field, "model entries must be forms")
		}
		if seen[name] {
			return "", Model{}, at(field, "duplicate model field %q", name)
		}
		seen[name] = true
		switch name {
		case "base-url":
			value, err := oneLiteral(field, values)
			if err != nil {
				return "", Model{}, err
			}
			m.BaseURL = value
		case "api-key-env":
			value, err := oneSymbol(field, values)
			if err != nil {
				return "", Model{}, err
			}
			m.APIKeyEnv = value
		case "id":
			value, err := oneLiteral(field, values)
			if err != nil {
				return "", Model{}, err
			}
			m.ID = value
		case "reasoning":
			value, err := oneOf(field, values, "none", "minimal", "low", "medium", "high", "xhigh")
			if err != nil {
				return "", Model{}, err
			}
			m.Reasoning = value
		case "max-tokens", "context-window":
			if len(values) != 1 || values[0].Kind != sexpr.Number || values[0].Integer <= 0 {
				return "", Model{}, at(field, "%s must be a positive integer", name)
			}
			if name == "max-tokens" {
				m.MaxTokens = int(values[0].Integer)
			} else {
				m.ContextWindow = int(values[0].Integer)
			}
		default:
			return "", Model{}, at(field, "unknown model field %q", name)
		}
	}
	if m.ID == "" {
		return "", Model{}, at(node, "model %q is missing id", name)
	}
	if m.APIKeyEnv == "" {
		return "", Model{}, at(node, "model %q is missing api-key-env", name)
	}
	return name, m, nil
}

func parseOrchestrator(node sexpr.Node, args []sexpr.Node, models map[string]Model) (Orchestrator, error) {
	o := Orchestrator{}
	seen := map[string]bool{}
	for _, field := range args {
		name, values, ok := field.Form()
		if !ok {
			return Orchestrator{}, at(field, "orchestrator entries must be forms")
		}
		if seen[name] {
			return Orchestrator{}, at(field, "duplicate orchestrator field %q", name)
		}
		seen[name] = true
		switch name {
		case "model":
			value, err := oneSymbol(field, values)
			if err != nil {
				return Orchestrator{}, err
			}
			o.Model = value
		case "prompt":
			value, err := oneLiteral(field, values)
			if err != nil {
				return Orchestrator{}, err
			}
			o.Prompt = value
		default:
			return Orchestrator{}, at(field, "unknown orchestrator field %q", name)
		}
	}
	if o.Model == "" {
		return Orchestrator{}, at(node, "orchestrator is missing model")
	}
	if strings.TrimSpace(o.Prompt) == "" {
		return Orchestrator{}, at(node, "orchestrator is missing prompt")
	}
	if _, ok := models[o.Model]; !ok {
		return Orchestrator{}, at(node, "orchestrator uses unknown model %q", o.Model)
	}
	return o, nil
}

func parseSkill(node sexpr.Node, args []sexpr.Node) (Skill, error) {
	if len(args) < 1 || args[0].Kind != sexpr.Symbol || !sexpr.ValidName(args[0].Value) {
		return Skill{}, at(node, "skill requires a valid symbolic name")
	}
	s := Skill{Name: args[0].Value}
	seen := map[string]bool{}
	for _, field := range args[1:] {
		name, values, ok := field.Form()
		if !ok {
			return Skill{}, at(field, "skill entries must be forms")
		}
		if seen[name] {
			return Skill{}, at(field, "duplicate skill field %q", name)
		}
		seen[name] = true
		value, err := oneLiteral(field, values)
		if err != nil {
			return Skill{}, err
		}
		switch name {
		case "description":
			s.Description = value
		case "instructions":
			s.Instructions = value
		default:
			return Skill{}, at(field, "unknown skill field %q", name)
		}
	}
	if strings.TrimSpace(s.Description) == "" {
		return Skill{}, at(node, "skill %q is missing description", s.Name)
	}
	if strings.TrimSpace(s.Instructions) == "" {
		return Skill{}, at(node, "skill %q is missing instructions", s.Name)
	}
	return s, nil
}

func parseRunner(node sexpr.Node, args []sexpr.Node, constants map[string]string) (string, Runner, map[string]parameter, error) {
	if len(args) < 1 || args[0].Kind != sexpr.Symbol {
		return "", Runner{}, nil, at(node, "runner requires a symbolic name")
	}
	name := args[0].Value
	r := Runner{Stderr: "log", SuccessExit: 0}
	parameters := map[string]parameter{}
	if !sexpr.ValidName(name) {
		return "", Runner{}, nil, at(args[0], "invalid runner name %q", name)
	}
	fields := map[string]bool{}
	var commandNodes, argumentNodes []sexpr.Node
	for _, field := range args[1:] {
		fieldName, values, ok := field.Form()
		if !ok {
			return "", Runner{}, nil, at(field, "runner entries must be forms")
		}
		if fields[fieldName] {
			return "", Runner{}, nil, at(field, "duplicate runner field %q", fieldName)
		}
		fields[fieldName] = true
		switch fieldName {
		case "parameters":
			for _, declaration := range values {
				paramName, param, err := parseParameter(declaration)
				if err != nil {
					return "", Runner{}, nil, err
				}
				if _, exists := parameters[paramName]; exists {
					return "", Runner{}, nil, at(declaration, "duplicate parameter %q", paramName)
				}
				parameters[paramName] = param
			}
		case "command":
			if len(values) == 0 {
				return "", Runner{}, nil, at(field, "command requires at least one argument")
			}
			commandNodes = values
		case "arguments":
			argumentNodes = values
		case "stderr":
			value, err := oneOf(field, values, "log", "merge", "discard")
			if err != nil {
				return "", Runner{}, nil, err
			}
			r.Stderr = value
		case "result":
			result, err := parseResult(field, values)
			if err != nil {
				return "", Runner{}, nil, err
			}
			r.Result = result
		case "success":
			exit, err := parseSuccess(field, values)
			if err != nil {
				return "", Runner{}, nil, err
			}
			r.SuccessExit = exit
		case "timeout":
			value, err := oneLiteral(field, values)
			if err != nil {
				return "", Runner{}, nil, err
			}
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return "", Runner{}, nil, at(field, "timeout must be a positive duration")
			}
			r.Timeout = d
		default:
			return "", Runner{}, nil, at(field, "unknown runner field %q", fieldName)
		}
	}
	if len(commandNodes) == 0 {
		return "", Runner{}, nil, at(node, "runner %q is missing command", name)
	}
	if r.Result == "" {
		return "", Runner{}, nil, at(node, "runner %q is missing result", name)
	}
	resolve := func(n sexpr.Node) (Arg, error) { return parseArg(n, constants, parameters) }
	for _, n := range commandNodes {
		a, err := resolve(n)
		if err != nil {
			return "", Runner{}, nil, err
		}
		r.Command = append(r.Command, a)
	}
	for _, n := range argumentNodes {
		arg, err := parseArgument(n, constants, parameters)
		if err != nil {
			return "", Runner{}, nil, err
		}
		r.Arguments = append(r.Arguments, arg)
	}
	if r.Command[0].Slot != "" {
		return "", Runner{}, nil, at(commandNodes[0], "command executable must resolve to a literal")
	}
	return name, r, parameters, nil
}

func parseParameter(node sexpr.Node) (string, parameter, error) {
	_, args, ok := node.Form()
	if !ok {
		return "", parameter{}, at(node, "parameter declaration must be a form")
	}
	name, _, _ := node.Form()
	if len(args) < 2 || args[0].Kind != sexpr.Symbol || args[0].Value != "string" || args[1].Kind != sexpr.Symbol {
		return "", parameter{}, at(node, "parameter syntax is (name string required|optional [default])")
	}
	p := parameter{}
	if !sexpr.ValidName(name) {
		return "", parameter{}, at(node, "invalid parameter name %q", name)
	}
	switch args[1].Value {
	case "required":
		p.Required = true
		if len(args) != 2 {
			return "", parameter{}, at(node, "required parameter cannot have a default")
		}
	case "optional":
		if len(args) > 3 {
			return "", parameter{}, at(node, "optional parameter accepts at most one default")
		}
		if len(args) == 3 {
			value, err := literal(args[2])
			if err != nil {
				return "", parameter{}, err
			}
			p.Default = &value
		}
	default:
		return "", parameter{}, at(args[1], "parameter must be required or optional")
	}
	return name, p, nil
}

func parseArgument(node sexpr.Node, constants map[string]string, params map[string]parameter) (Argument, error) {
	if head, args, ok := node.Form(); ok {
		if head != "optional" || len(args) < 2 || args[0].Kind != sexpr.Symbol {
			return Argument{}, at(node, "argument form must be (optional parameter ARG...)")
		}
		name := args[0].Value
		param, exists := params[name]
		if !exists || param.Required {
			return Argument{}, at(args[0], "optional condition %q is not an optional parameter", name)
		}
		out := Argument{When: name}
		for _, value := range args[1:] {
			a, err := parseArg(value, constants, params)
			if err != nil {
				return Argument{}, err
			}
			out.Args = append(out.Args, a)
		}
		return out, nil
	}
	a, err := parseArg(node, constants, params)
	return Argument{Args: []Arg{a}}, err
}

func parseArg(node sexpr.Node, constants map[string]string, params map[string]parameter) (Arg, error) {
	switch node.Kind {
	case sexpr.String:
		return Arg{Literal: node.Value}, nil
	case sexpr.Number:
		return Arg{Literal: strconv.FormatInt(node.Integer, 10)}, nil
	case sexpr.Symbol:
		if value, ok := constants[node.Value]; ok {
			return Arg{Literal: value}, nil
		}
		if runtimeSlots[node.Value] {
			return Arg{Slot: node.Value}, nil
		}
		if _, ok := params[node.Value]; ok {
			return Arg{Slot: node.Value}, nil
		}
		return Arg{}, at(node, "unknown argument symbol %q", node.Value)
	default:
		return Arg{}, at(node, "command arguments must be literals or value symbols")
	}
}

func parseResult(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 {
		return "", at(node, "result requires stdout or (file result-file)")
	}
	if args[0].Kind == sexpr.Symbol && args[0].Value == "stdout" {
		return "stdout", nil
	}
	head, values, ok := args[0].Form()
	if !ok || head != "file" || len(values) != 1 || values[0].Kind != sexpr.Symbol || values[0].Value != "result-file" {
		return "", at(args[0], "result requires stdout or (file result-file)")
	}
	return "file", nil
}

func parseSuccess(node sexpr.Node, args []sexpr.Node) (int, error) {
	if len(args) != 1 {
		return 0, at(node, "success requires (exit CODE)")
	}
	head, values, ok := args[0].Form()
	if !ok || head != "exit" || len(values) != 1 || values[0].Kind != sexpr.Number || values[0].Integer < 0 || values[0].Integer > 255 {
		return 0, at(args[0], "success requires an exit code from 0 to 255")
	}
	return int(values[0].Integer), nil
}

func parseAgent(node sexpr.Node, args []sexpr.Node, runners map[string]Runner, runnerParameters map[string]map[string]parameter) (string, Agent, error) {
	if len(args) < 1 || args[0].Kind != sexpr.Symbol {
		return "", Agent{}, at(node, "agent requires a symbolic name")
	}
	agentName := args[0].Value
	a := Agent{Parameters: map[string]string{}}
	if !sexpr.ValidName(agentName) {
		return "", Agent{}, at(args[0], "invalid agent name %q", agentName)
	}
	fields := map[string]bool{}
	var paramNodes []sexpr.Node
	for _, field := range args[1:] {
		name, values, ok := field.Form()
		if !ok {
			return "", Agent{}, at(field, "agent entries must be forms")
		}
		if fields[name] {
			return "", Agent{}, at(field, "duplicate agent field %q", name)
		}
		fields[name] = true
		switch name {
		case "using":
			value, err := oneSymbol(field, values)
			if err != nil {
				return "", Agent{}, err
			}
			a.Runner = value
		default:
			paramNodes = append(paramNodes, field)
		}
	}
	if a.Runner == "" {
		return "", Agent{}, at(node, "agent %q is missing using", agentName)
	}
	if _, exists := runners[a.Runner]; !exists {
		return "", Agent{}, at(node, "agent %q uses unknown runner %q", agentName, a.Runner)
	}
	parameters := runnerParameters[a.Runner]
	for _, field := range paramNodes {
		name, values, _ := field.Form()
		if _, exists := parameters[name]; !exists {
			return "", Agent{}, at(field, "unknown parameter %q for runner %q", name, a.Runner)
		}
		value, err := oneLiteral(field, values)
		if err != nil {
			return "", Agent{}, err
		}
		a.Parameters[name] = value
	}
	for name, param := range parameters {
		if _, set := a.Parameters[name]; set {
			continue
		}
		if param.Default != nil {
			a.Parameters[name] = *param.Default
		} else if param.Required {
			return "", Agent{}, at(node, "agent %q is missing required runner parameter %q", agentName, name)
		}
	}
	return agentName, a, nil
}

func oneSymbol(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 || args[0].Kind != sexpr.Symbol {
		return "", at(node, "form requires one symbol")
	}
	return args[0].Value, nil
}

func oneLiteral(node sexpr.Node, args []sexpr.Node) (string, error) {
	if len(args) != 1 {
		return "", at(node, "form requires one literal value")
	}
	return literal(args[0])
}

func oneOf(node sexpr.Node, args []sexpr.Node, allowed ...string) (string, error) {
	value, err := oneSymbol(node, args)
	if err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", at(node, "value %q must be one of %v", value, allowed)
}

func literal(node sexpr.Node) (string, error) {
	switch node.Kind {
	case sexpr.String:
		return node.Value, nil
	case sexpr.Number:
		return strconv.FormatInt(node.Integer, 10), nil
	default:
		return "", at(node, "value must be a string or number literal")
	}
}

func at(node sexpr.Node, format string, args ...any) error {
	return fmt.Errorf("%s: %s", node.Pos, fmt.Sprintf(format, args...))
}
