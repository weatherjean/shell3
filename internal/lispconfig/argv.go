package lispconfig

import "fmt"

// Argv resolves one configured agent and the invocation's runtime slots to an
// exact argv vector. No result is interpreted by a shell.
func (c *Config) Argv(agentName string, slots map[string]string) ([]string, error) {
	a, ok := c.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("config: unknown agent %q", agentName)
	}
	r := c.Runners[a.Runner]
	values := make(map[string]string, len(slots)+len(a.Parameters))
	for k, v := range slots {
		values[k] = v
	}
	for k, v := range a.Parameters {
		values[k] = v
	}
	resolve := func(arg Arg) (string, error) {
		if arg.Slot == "" {
			return arg.Literal, nil
		}
		value, exists := values[arg.Slot]
		if !exists {
			return "", fmt.Errorf("config: agent %q invocation is missing %q", agentName, arg.Slot)
		}
		return value, nil
	}
	argv := make([]string, 0, len(r.Command)+len(r.Arguments)*2)
	for _, arg := range r.Command {
		value, err := resolve(arg)
		if err != nil {
			return nil, err
		}
		argv = append(argv, value)
	}
	for _, expression := range r.Arguments {
		if expression.When != "" && values[expression.When] == "" {
			continue
		}
		for _, arg := range expression.Args {
			value, err := resolve(arg)
			if err != nil {
				return nil, err
			}
			argv = append(argv, value)
		}
	}
	return argv, nil
}
