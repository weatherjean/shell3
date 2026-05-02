package patchapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// SlashHandler is invoked when the user submits a registered "/cmd args"
// line. args is the substring after the command name with surrounding
// whitespace trimmed; "" if none was provided.
type SlashHandler func(args string)

// SlashCommand describes one entry in the slash-command registry.
//
// Aliases share the same handler and Help text and appear together in
// the auto-generated /help output. Names and aliases are matched
// case-insensitively.
type SlashCommand struct {
	Name    string       // canonical name without leading "/"
	Aliases []string     // optional alternates without leading "/"
	Help    string       // one-line description for /help
	Handler SlashHandler // required
}

// RegisterSlash adds a command to the dispatch table. Re-registering an
// existing name (or alias) replaces the prior entry. The reserved name
// "help" is auto-handled and printed via [App.Print]; callers may
// override by registering their own /help, in which case the auto
// listing is skipped.
func (a *App) RegisterSlash(cmd SlashCommand) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.slash == nil {
		a.slash = make(map[string]*SlashCommand)
	}
	c := cmd
	for _, n := range append([]string{c.Name}, c.Aliases...) {
		a.slash[strings.ToLower(n)] = &c
	}
}

// dispatchSlash looks up the command from a "/cmd args" input. Returns
// true if a registered command (or the auto /help) handled it.
func (a *App) dispatchSlash(input string) bool {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	body := trimmed[1:]
	name, args, _ := strings.Cut(body, " ")
	args = strings.TrimSpace(args)
	key := strings.ToLower(name)

	a.mu.Lock()
	cmd, ok := a.slash[key]
	hasHelpOverride := false
	if _, exists := a.slash["help"]; exists {
		hasHelpOverride = true
	}
	a.mu.Unlock()

	if ok {
		cmd.Handler(args)
		a.Refresh()
		return true
	}
	// Auto /help when nothing else handles "help" or its short aliases.
	if !hasHelpOverride {
		switch key {
		case "help", "h", "list", "":
			a.printAutoHelp()
			a.Refresh()
			return true
		}
	}
	a.PrintLine(patchtui.Dim + fmt.Sprintf("[unknown command: /%s  (type /help to list commands)]", name) + patchtui.Reset)
	a.Refresh()
	return true
}

// printAutoHelp renders the registered command list, dedup'd by canonical
// name and sorted alphabetically. Aliases listed inline.
func (a *App) printAutoHelp() {
	a.mu.Lock()
	seen := make(map[string]*SlashCommand)
	for _, c := range a.slash {
		seen[c.Name] = c
	}
	a.mu.Unlock()

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	lines := []string{
		"",
		patchtui.Yellow + patchtui.Bold + "Slash commands" + patchtui.Reset,
		patchtui.Dim + "Run a command by typing it at the prompt." + patchtui.Reset,
		"",
	}
	for _, n := range names {
		c := seen[n]
		cmdCol := fmt.Sprintf("  %-24s", "/"+c.Name)
		if len(c.Aliases) > 0 {
			plainDisplay := "/" + c.Name + " (/" + strings.Join(c.Aliases, ", /") + ")"
			cmdCol = fmt.Sprintf("  %-24s", plainDisplay)
		}
		lines = append(lines, patchtui.Cyan+patchtui.Bold+cmdCol+patchtui.Reset+"  "+patchtui.Dim+c.Help+patchtui.Reset)
	}
	lines = append(lines, "",
		patchtui.Yellow+patchtui.Bold+"Keyboard shortcuts"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  enter          "+patchtui.Reset+patchtui.Dim+"send message"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  alt+enter      "+patchtui.Reset+patchtui.Dim+"newline in message"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  esc            "+patchtui.Reset+patchtui.Dim+"clear input"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  ctrl+c         "+patchtui.Reset+patchtui.Dim+"cancel active response"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  ctrl+c ctrl+c  "+patchtui.Reset+patchtui.Dim+"quit (when idle)"+patchtui.Reset,
		"",
		patchtui.Yellow+patchtui.Bold+"Shell passthrough"+patchtui.Reset,
		patchtui.Cyan+patchtui.Bold+"  !<cmd>         "+patchtui.Reset+patchtui.Dim+"run shell command with full terminal"+patchtui.Reset,
		"",
	)
	a.Print(lines)
}
