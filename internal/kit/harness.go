package kit

import (
	"fmt"
	"sort"
	"strings"
)

// harnessPrelude is the bash sourced before a kit's test functions run. It
// defines the five names a test may use and nothing else: tool, stub,
// assert_eq, assert_contains, fail. $KIT_TMP is exported by the caller.
const harnessPrelude = `
_kit_fail_count=0

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  if [ "$1" != "$2" ]; then
    printf 'FAIL: expected %q, got %q\n' "$2" "$1" >&2
    exit 1
  fi
}

assert_contains() {
  case "$1" in
    *"$2"*) : ;;
    *) printf 'FAIL: %q does not contain %q\n' "$1" "$2" >&2; exit 1 ;;
  esac
}

# stub NAME  — read stdin and install NAME at the front of PATH as a command
# that prints exactly that, whatever arguments it is given. You are not testing
# that curl works; you are testing what your tool does with curl's output.
stub() {
  local name="$1"
  cat > "$KIT_STUBS/$name.out"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'cat %q\n' "$KIT_STUBS/$name.out"
  } > "$KIT_STUBS/$name"
  chmod +x "$KIT_STUBS/$name"
}
`

// toolDispatch renders the `tool` helper: a case statement mapping declared
// tool names onto their functions, applying declared defaults, and running the
// call in a subshell so exported params never leak between assertions.
func toolDispatch(tools []Tool) string {
	var b strings.Builder
	b.WriteString("\ntool() {\n  local _name=\"$1\"; shift\n  (\n")
	b.WriteString("    for _kv in \"$@\"; do export \"${_kv?}\"; done\n")
	b.WriteString("    case \"$_name\" in\n")

	for _, t := range tools {
		fmt.Fprintf(&b, "      %s)\n", shellQuote(t.Name))

		names := make([]string, 0, len(t.Params))
		for n := range t.Params {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := t.Params[n]
			switch {
			case p.Default != nil:
				fmt.Fprintf(&b, "        : \"${%s:=%v}\"\n", n, p.Default)
			case p.Required:
				fmt.Fprintf(&b, "        [ -n \"${%s:-}\" ] || { echo 'tool %s: missing required argument %s' >&2; exit 1; }\n",
					n, t.Name, n)
			default:
				fmt.Fprintf(&b, "        : \"${%s:=}\"\n", n)
			}
		}
		fmt.Fprintf(&b, "        %s\n        ;;\n", t.Func)
	}

	b.WriteString("      *) echo \"unknown tool: $_name\" >&2; exit 1 ;;\n")
	b.WriteString("    esac\n  )\n}\n")
	return b.String()
}

// TestScript renders the full bash program that runs one agent's tests: the
// harness, the tool dispatcher for everything that agent can call, a source of
// the kit, and the selected test functions.
//
// only, when non-empty, restricts the run to tests whose declared name has that
// prefix — the `shell3 tool test <kit> <tool>` form.
func (k *Kit) TestScript(a Agent, only string) (string, int) {
	var b strings.Builder
	b.WriteString("set -uo pipefail\n")
	b.WriteString(harnessPrelude)
	b.WriteString(toolDispatch(k.toolsFor(a)))

	n := 0
	for _, t := range a.Tests {
		if only != "" && !strings.HasPrefix(t.Name, only) {
			continue
		}
		n++
		fmt.Fprintf(&b, "\nprintf '%%s ... ' %s\n", shellQuote(t.Name))
		fmt.Fprintf(&b, "if ( %s ); then printf 'ok\\n'; else _kit_fail_count=$((_kit_fail_count+1)); fi\n", t.Func)
	}
	b.WriteString("\nexit $((_kit_fail_count > 0))\n")
	return b.String(), n
}

// toolsFor is every tool an agent can call: its own, plus those from the shared
// groups it imports via use:.
func (k *Kit) toolsFor(a Agent) []Tool {
	out := append([]Tool{}, a.Tools...)
	for _, name := range a.Use {
		for _, g := range k.Shared {
			if g.Name == name {
				out = append(out, g.Tools...)
			}
		}
	}
	return out
}
