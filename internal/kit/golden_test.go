package kit

import (
	"os"
	"os/exec"
	"testing"
)

func TestParseAmpdFixture(t *testing.T) {
	src, err := os.ReadFile("testdata/ampd.sh")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	k, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(k.Agents) != 2 {
		t.Fatalf("agents = %d, want 2 (main, ampd-leads)", len(k.Agents))
	}

	ampd := k.Agents[1]
	if ampd.Name != "ampd-leads" || ampd.Model != "sonnet" {
		t.Fatalf("agent[1] = %+v", ampd)
	}
	if len(ampd.Tools) != 1 || len(ampd.Tests) != 1 || len(ampd.Skills) != 1 {
		t.Fatalf("ampd tools/tests/skills = %d/%d/%d, want 1/1/1",
			len(ampd.Tools), len(ampd.Tests), len(ampd.Skills))
	}
	if ampd.Tools[0].Func != "ampd_stack_check" {
		t.Fatalf("stack-check binds %q", ampd.Tools[0].Func)
	}

	// The test block must bind its own function, not the tool's — this is the
	// binding-ceiling behaviour on a realistic file.
	if ampd.Tests[0].Func != "ampd_test_stack_check" {
		t.Fatalf("test binds %q, want ampd_test_stack_check", ampd.Tests[0].Func)
	}

	schema := ampd.Tools[0].Schema()
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "url" {
		t.Fatalf("stack-check required = %v, want [url]", req)
	}
	props, _ := schema["properties"].(map[string]any)
	timeout, _ := props["timeout"].(map[string]any)
	if timeout["type"] != "integer" || timeout["default"] != 20 {
		t.Fatalf("timeout prop = %#v", timeout)
	}

	if len(k.Shared) != 1 || len(k.Shared[0].Tools) != 1 {
		t.Fatalf("shared = %+v", k.Shared)
	}
}

// TestFixtureIsValidBash is the point of choosing .sh: the whole kit is
// checkable with one command, no extraction and no line remapping.
func TestFixtureIsValidBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command("bash", "-n", "testdata/ampd.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
}
