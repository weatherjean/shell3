package shell3_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

const reloadBaseCfg = `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
`

func writeReloadCfg(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Reload re-reads the config file and applies it in place: new agents appear,
// live sessions keep running, and the telegram/cron mirrors refresh.
func TestReloadPicksUpConfigChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lua")
	writeReloadCfg(t, path, reloadBaseCfg)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}

	writeReloadCfg(t, path, reloadBaseCfg+`
shell3.subagent({ name="second", description="d", model="main", prompt="p2", tools={} })
shell3.telegram({ token="tk", chat_id="42" })
`)
	res, err := rt.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents != 1 {
		t.Fatalf("expected 1 agent after reload, got %d (notes: %v)", res.Agents, res.Notes)
	}
	if rt.Telegram().ChatID != "42" {
		t.Fatalf("telegram mirror not refreshed: %+v", rt.Telegram())
	}
	if sess.Snapshot().Agent == "" {
		t.Fatal("live session unusable after reload")
	}
}

// A broken config must be rejected wholesale: Reload errors and the running
// runtime (and its sessions) keep the previous config.
func TestReloadRejectsBrokenConfigAndKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lua")
	writeReloadCfg(t, path, reloadBaseCfg)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}

	writeReloadCfg(t, path, "this is not lua (")
	if _, err := rt.Reload(); err == nil {
		t.Fatal("Reload must fail on a broken config")
	}
	if sess.Snapshot().Agent == "" {
		t.Fatal("session must stay usable after a failed reload")
	}
}

// Reload must re-apply the session decorator: it rebuilds every live
// session's cfg (dropping decorator-registered host tools like
// image_generate), so without re-application the tool would vanish after
// every /reload.
func TestReloadReappliesSessionDecorator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lua")
	writeReloadCfg(t, path, reloadBaseCfg)
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
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
