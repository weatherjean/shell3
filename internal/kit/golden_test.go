package kit

import (
	"os"
	"os/exec"
	"testing"
)

func TestParseBookmarksFixture(t *testing.T) {
	src, err := os.ReadFile("testdata/bookmarks.sh")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	k, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(k.Agents) != 2 {
		t.Fatalf("agents = %d, want 2 (main, bookmarks)", len(k.Agents))
	}

	bm := k.Agents[1]
	if bm.Name != "bookmarks" || bm.Model != "sonnet" {
		t.Fatalf("agent[1] = %+v", bm)
	}
	if len(bm.Tools) != 1 || len(bm.Tests) != 1 {
		t.Fatalf("bm tools/tests = %d/%d, want 1/1", len(bm.Tools), len(bm.Tests))
	}
	if bm.Tools[0].Func != "bm_page_kind" {
		t.Fatalf("page-kind binds %q", bm.Tools[0].Func)
	}

	// The test block must bind its own function, not the tool's — this is the
	// binding-ceiling behaviour on a realistic file.
	if bm.Tests[0].Func != "bm_test_page_kind" {
		t.Fatalf("test binds %q, want bm_test_page_kind", bm.Tests[0].Func)
	}

	schema := bm.Tools[0].Schema()
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "url" {
		t.Fatalf("page-kind required = %v, want [url]", req)
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
	out, err := exec.Command("bash", "-n", "testdata/bookmarks.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
}
