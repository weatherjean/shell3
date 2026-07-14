package shell3_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRuntime_TelegramConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.telegram({ token="bot-token", chat_id="42", dashboard={ enabled=true, addr="127.0.0.1:8765", url="https://h.ts.net/" } })
`
	path := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	tg := rt.Telegram()
	if tg.Token != "bot-token" || tg.ChatID != "42" || !tg.Dashboard.Enabled {
		t.Fatalf("bad telegram config: %+v", tg)
	}
}
