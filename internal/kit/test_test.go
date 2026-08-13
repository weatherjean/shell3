package kit

import (
	"context"
	"strings"
	"testing"
)

const testKit = `#---
# agent: a
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: stack-check
# description: classify a stack
# params:
#   url:     {type: string, required: true}
#   timeout: {type: int, default: 20}
#---
a_stack_check() {
  local html
  html=$(curl -sL --max-time "$timeout" "$url")
  if   grep -q 'shopify'   <<<"$html"; then echo shopify
  elif grep -q 'woocommerce' <<<"$html"; then echo wp_wc
  else echo none; fi
}

#---
# test: stack-check — classifies each stack
#---
a_test_stack_check() {
  stub curl <<'STUB'
<link href="/plugins/woocommerce/x.css">
STUB
  assert_eq "$(tool stack-check url=https://x.test)" wp_wc

  stub curl <<'STUB'
<html>a blog</html>
STUB
  assert_eq "$(tool stack-check url=https://x.test)" none
}
`

func TestRunTestsPasses(t *testing.T) {
	k := mustParse(t, testKit)
	path := writeKit(t, testKit)

	res, err := k.RunTests(context.Background(), path, k.Agents[0], "")
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if res.Ran != 1 {
		t.Fatalf("ran = %d, want 1", res.Ran)
	}
	if res.Failed {
		t.Fatalf("tests failed:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Fatalf("output = %q", res.Output)
	}
}

const failingKit = `#---
# agent: a
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: t
# description: echoes a fixed value
#---
a_t() { echo actual; }

#---
# test: t — wrong expectation
#---
a_test_t() { assert_eq "$(tool t)" expected; }
`

func TestRunTestsReportsFailure(t *testing.T) {
	k := mustParse(t, failingKit)
	path := writeKit(t, failingKit)

	res, err := k.RunTests(context.Background(), path, k.Agents[0], "")
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if !res.Failed {
		t.Fatalf("want a failure, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "expected") {
		t.Fatalf("failure output should name the expectation: %q", res.Output)
	}
}

// A required argument the test forgets must be caught by the dispatcher, not
// silently passed through as an empty string.
func TestToolDispatchEnforcesRequired(t *testing.T) {
	src := `#---
# agent: a
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: need
# description: needs an arg
# params:
#   x: {type: string, required: true}
#---
a_need() { echo "got:$x"; }

#---
# test: need — forgets the arg
#---
a_test_need() { assert_contains "$(tool need 2>&1)" "missing required"; }
`
	k := mustParse(t, src)
	res, err := k.RunTests(context.Background(), writeKit(t, src), k.Agents[0], "")
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if res.Failed {
		t.Fatalf("dispatcher did not enforce required args:\n%s", res.Output)
	}
}

// Defaults declared in the manifest must apply when the caller omits them.
func TestToolDispatchAppliesDefaults(t *testing.T) {
	src := `#---
# agent: a
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: d
# description: has a default
# params:
#   n: {type: int, default: 7}
#---
a_d() { echo "n=$n"; }

#---
# test: d — default applies
#---
a_test_d() { assert_eq "$(tool d)" "n=7"; }
`
	k := mustParse(t, src)
	res, err := k.RunTests(context.Background(), writeKit(t, src), k.Agents[0], "")
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if res.Failed {
		t.Fatalf("defaults not applied:\n%s", res.Output)
	}
}

// Shared-group tools an agent imports must be callable from its tests.
func TestToolDispatchIncludesSharedTools(t *testing.T) {
	src := `#---
# agent: a
# use: [web]
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# test: search — shared tool is callable
#---
a_test_search() { assert_eq "$(tool search q=hi)" "searched:hi"; }

#---
# shared: web
#---
#---
# tool: search
# description: search
# params:
#   q: {type: string, required: true}
#---
web_search() { echo "searched:$q"; }
`
	k := mustParse(t, src)
	res, err := k.RunTests(context.Background(), writeKit(t, src), k.Agents[0], "")
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if res.Failed {
		t.Fatalf("shared tool not reachable from tests:\n%s", res.Output)
	}
}
