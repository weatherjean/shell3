package shell3

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// writeProjectConfig writes a minimal config tree with one project ("site")
// whose manager runs in `work`, so BuildParts produces a Parts whose
// SubagentWorkdir("site") == work.
func writeProjectConfig(t *testing.T, dir, work string) {
	t.Helper()
	files := map[string]string{
		".env":        "TEST_KEY=sk-test\n",
		"shell3.yaml": "models:\n  main:\n    base_url: https://example.test/v1\n    api_key: env:TEST_KEY\n    model: test-model\n    context_window: 1000\n",
		"agent.md":    "---\nmodel: main\ntools: [bash]\n---\nyou are a coder\n",

		"projects/site/project.md": "---\ndescription: my site\nworkdir: " + work + "\n---\nBrief.\n",
		"projects/site/manager.md": "---\ndescription: manages site\n---\nYou are the site manager.\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStartSubagent_ManagerWorkdir proves the jobs seam: when startSubagent
// spawns a project manager with no explicit spawn-time workdir, the child
// session's shell runs in the project's declared workdir (resolved via
// rt.Parts().SubagentWorkdir). An ordinary spawn (no matching project) keeps
// the parent's workdir.
func TestStartSubagent_ManagerWorkdir(t *testing.T) {
	cfgDir := t.TempDir()
	work := t.TempDir()
	writeProjectConfig(t, cfgDir, work)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: cfgDir,
		CWD:       cfgDir,
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	rt := newTestRuntime(t, fakeCfg("done"))
	// Inject real Parts so the seam's rt.Parts().SubagentWorkdir(agent) resolves
	// the project manager's declared workdir.
	rt.parts = parts

	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent session: %v", err)
	}

	// Wrap sessionConfig to capture the WorkDir each child session is built
	// with (write-once at construction; a mutex keeps the race detector happy).
	var mu sync.Mutex
	var lastWorkDir string
	base := rt.sessionConfig
	rt.sessionConfig = func(o SessionOpts) (chat.Config, error) {
		mu.Lock()
		lastWorkDir = o.WorkDir
		mu.Unlock()
		return base(o)
	}

	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastWorkDir
	}

	// Spawn the project manager: no explicit spawn workdir → project workdir.
	id, err := rt.jobs.startSubagent(parent, "site", "do the thing", "manage site", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent(site): %v", err)
	}
	waitForWake(t, rt, parent)
	if got := read(); got != work {
		t.Errorf("manager child WorkDir = %q, want %q (project workdir)", got, work)
	}
	_ = id

	// Spawn an ordinary agent (no project match) → keeps the parent's workdir
	// (parent has "" → runtime root downstream), proving the seam is scoped to
	// project managers.
	if _, err := rt.jobs.startSubagent(parent, "explorer", "look around", "explore", subagentOpts{}); err != nil {
		t.Fatalf("startSubagent(explorer): %v", err)
	}
	waitForWake(t, rt, parent)
	if got := read(); got != "" {
		t.Errorf("ordinary child WorkDir = %q, want \"\" (inherit parent)", got)
	}

	// An explicit spawn-time workdir still wins over the project's declared one.
	explicit := t.TempDir()
	if _, err := rt.jobs.startSubagent(parent, "site", "deploy", "deploy site", subagentOpts{workDir: explicit}); err != nil {
		t.Fatalf("startSubagent(site, explicit): %v", err)
	}
	waitForWake(t, rt, parent)
	if got := read(); got != explicit {
		t.Errorf("explicit spawn WorkDir = %q, want %q (explicit wins over project)", got, explicit)
	}
}
