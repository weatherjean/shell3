package shell3_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func writeReloadCfg(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadPicksUpConfigChange(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, nil)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}

	writeTreeFiles(t, dir, map[string]string{
		"shell3.sh": baseWiring[:len(baseWiring)-len("#---\n")] +
			"#   telegram:\n#     chat_id: \"123456789\"\n#---\n" +
			kitAgentDecl("main", "hi") + kitAgentDecl("second", "p2", "description: d"),
	})
	res, err := rt.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents != 2 {
		t.Fatalf("expected 2 agents after reload, got %d (notes: %v)", res.Agents, res.Notes)
	}
	if rt.Telegram().ChatID != "123456789" {
		t.Fatalf("telegram config not refreshed: %+v", rt.Telegram())
	}
	if sess.Snapshot().Agent == "" {
		t.Fatal("live session unusable after reload")
	}
}

func TestReloadRejectsBrokenConfigAndKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, nil)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}

	writeReloadCfg(t, filepath.Join(dir, "shell3.sh"), "#---\n# shell3:\n#   models: [not, a, map\n#---\n")
	if _, err := rt.Reload(); err == nil {
		t.Fatal("Reload must fail on a broken config")
	}
	if sess.Snapshot().Agent == "" {
		t.Fatal("session must stay usable after a failed reload")
	}
}

func TestReloadReappliesSessionDecorator(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, nil)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	rt.SetSessionDecorator(func(s *shell3.Session) {
		_ = s.RegisterHostTool(shell3.HostTool{
			Name:       "image_generate",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler:    func(ctx context.Context, argsJSON string) (string, error) { return "ok", nil },
		})
	})
	sess, err := rt.Session(shell3.SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		n := 0
		for _, ti := range sess.Snapshot().Tools {
			if ti.Name == "image_generate" {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("before reload: image_generate registered %d times, want 1", count())
	}
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("after reload: image_generate registered %d times, want exactly 1 (dropped or duplicated)", count())
	}
}
