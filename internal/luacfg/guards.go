package luacfg

import (
	"regexp"

	lua "github.com/yuin/gopher-lua"
)

// registerGuards registers shell3.guards.* constructors into the shell3 table.
func registerGuards(c *LoadedConfig, tbl *lua.LTable) {
	L := c.L
	g := L.NewTable()
	L.SetField(g, "confirm_dangerous", L.NewFunction(func(L *lua.LState) int {
		prompt := false
		if o, ok := L.Get(1).(*lua.LTable); ok {
			prompt = optBool(o, "prompt")
		}
		h := L.NewTable()
		h.RawSetString("__guard", lua.LString("confirm_dangerous"))
		h.RawSetString("prompt", lua.LBool(prompt))
		L.Push(h)
		return 1
	}))
	L.SetField(tbl, "guards", g)
}

// dangerPatterns is the full denylist ported from ~/.shell3/hooks/confirm-bash.sh.
// Each entry is an ERE (Go RE2 compatible). On match → DecisionBlock.
var dangerPatterns = []*regexp.Regexp{
	// File deletion / truncation
	regexp.MustCompile(`(^|[[:space:];|&])rm[[:space:]]`),
	regexp.MustCompile(`(^|[[:space:];|&])rmdir[[:space:]]`),
	regexp.MustCompile(`(^|[[:space:];|&])shred([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])truncate([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])unlink([[:space:]]|$)`),

	// Disk / filesystem
	regexp.MustCompile(`(^|[[:space:];|&])dd[[:space:]].*[[:space:]]of=`),
	regexp.MustCompile(`(^|[[:space:];|&])mkfs(\.[a-z0-9]+)?([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])wipefs([[:space:]]|$)`),

	// Privilege elevation
	regexp.MustCompile(`(^|[[:space:];|&])sudo([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])su[[:space:]]`),
	regexp.MustCompile(`(^|[[:space:];|&])doas([[:space:]]|$)`),

	// Broad permission / ownership changes
	regexp.MustCompile(`(^|[[:space:];|&])chmod[[:space:]]+[+-]?[0-7]*7[0-7]*7`),
	regexp.MustCompile(`(^|[[:space:];|&])chmod[[:space:]]+-R`),
	regexp.MustCompile(`(^|[[:space:];|&])chown[[:space:]]+-R`),

	// Irreversible VCS
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+push[[:space:]].*(--force|-f([[:space:]]|$)|--mirror|--delete)`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+reset[[:space:]]+--hard`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+clean[[:space:]]+-[a-zA-Z]*[fF]`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+branch[[:space:]]+-D`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+checkout[[:space:]]+--([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+restore[[:space:]]+--source`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+filter-branch`),
	regexp.MustCompile(`(^|[[:space:];|&])git[[:space:]]+update-ref[[:space:]]+-d`),

	// Package managers (uninstall)
	regexp.MustCompile(`(^|[[:space:];|&])npm[[:space:]]+(uninstall|rm|remove)([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])pnpm[[:space:]]+remove([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])yarn[[:space:]]+remove([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])pip[[:space:]]+uninstall`),
	regexp.MustCompile(`(^|[[:space:];|&])brew[[:space:]]+(uninstall|remove)([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])apt(-get)?[[:space:]]+(remove|purge|autoremove)`),
	regexp.MustCompile(`(^|[[:space:];|&])yum[[:space:]]+(remove|erase)`),
	regexp.MustCompile(`(^|[[:space:];|&])dnf[[:space:]]+(remove|erase)`),
	regexp.MustCompile(`(^|[[:space:];|&])pacman[[:space:]]+-R`),
	regexp.MustCompile(`(^|[[:space:];|&])go[[:space:]]+clean[[:space:]]+-modcache`),
	regexp.MustCompile(`(^|[[:space:];|&])cargo[[:space:]]+clean([[:space:]]|$)`),

	// SQL destructive
	regexp.MustCompile(`DROP[[:space:]]+(TABLE|DATABASE|SCHEMA|INDEX)`),
	regexp.MustCompile(`TRUNCATE[[:space:]]+TABLE`),
	regexp.MustCompile(`DELETE[[:space:]]+FROM`),

	// Pipe-to-shell
	regexp.MustCompile(`(curl|wget)[[:space:]][^|]*\|[[:space:]]*(sudo[[:space:]]+)?(bash|sh|zsh)([[:space:]]|$)`),

	// System control
	regexp.MustCompile(`(^|[[:space:];|&])systemctl[[:space:]]+(stop|disable|mask)`),
	regexp.MustCompile(`(^|[[:space:];|&])service[[:space:]]+[^[:space:]]+[[:space:]]+stop`),
	regexp.MustCompile(`(^|[[:space:];|&])shutdown([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])reboot([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])halt([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])kill[[:space:]]+-9`),
	regexp.MustCompile(`(^|[[:space:];|&])killall([[:space:]]|$)`),
	regexp.MustCompile(`(^|[[:space:];|&])pkill([[:space:]]|$)`),

	// Firewall
	regexp.MustCompile(`(^|[[:space:];|&])iptables[[:space:]]+-F`),
	regexp.MustCompile(`(^|[[:space:];|&])nft[[:space:]]+flush`),

	// Docker / container — volume and bulk deletes
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+volume[[:space:]]+(rm|remove)`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+volume[[:space:]]+prune`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+system[[:space:]]+prune`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+(container[[:space:]]+)?prune`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+image[[:space:]]+prune`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+network[[:space:]]+prune`),
	regexp.MustCompile(`(^|[[:space:];|&])docker[[:space:]]+rm[[:space:]].*-[a-zA-Z]*v`),
	regexp.MustCompile(`(^|[[:space:];|&])docker([[:space:]]+container)?[[:space:]]+rm[[:space:]]+-f`),
	regexp.MustCompile(`(^|[[:space:];|&])docker(-| )compose[[:space:]]+down[[:space:]].*-[a-zA-Z]*v`),
	regexp.MustCompile(`(^|[[:space:];|&])docker(-| )compose[[:space:]]+rm`),
	regexp.MustCompile(`(^|[[:space:];|&])podman[[:space:]]+volume[[:space:]]+(rm|prune)`),
	regexp.MustCompile(`(^|[[:space:];|&])podman[[:space:]]+system[[:space:]]+prune`),

	// Risky redirects
	regexp.MustCompile(`>[[:space:]]*/etc/`),
	regexp.MustCompile(`>[[:space:]]*/dev/sd[a-z]`),
	regexp.MustCompile(`>[[:space:]]*/dev/nvme`),
	regexp.MustCompile(`>[[:space:]]*/dev/disk`),
	regexp.MustCompile(`>[[:space:]]*~/\.[a-zA-Z]`),

	// Fork bomb
	regexp.MustCompile(`:\([[:space:]]*\)[[:space:]]*\{`),
}

// runBuiltinGuard evaluates a built-in guard (currently only "confirm_dangerous").
// Non-shell tools are always allowed. Shell commands matching dangerPatterns → DecisionBlock.
func runBuiltinGuard(g GuardEntry, tool string, params map[string]any) (Decision, string) {
	if g.Builtin != "confirm_dangerous" {
		return DecisionAllow, ""
	}
	switch tool {
	case "bash", "bash_bg", "shell_interactive":
	default:
		return DecisionAllow, ""
	}
	cmd, _ := params["command"].(string)
	for _, re := range dangerPatterns {
		if re.MatchString(cmd) {
			return DecisionBlock, "blocked dangerous command (confirm_dangerous guard)"
		}
	}
	return DecisionAllow, ""
}
