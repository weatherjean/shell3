package shell3_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRuntime_TelegramConfig(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, map[string]string{
		"shell3.yaml": baseYAML + `telegram:
  token: bot-token
  chat_id: "42"
  dashboard: { addr: "127.0.0.1:8765", url: "https://h.ts.net/" }
`,
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	tg := rt.Telegram()
	if tg.Token != "bot-token" || tg.ChatID != "42" || !tg.Dashboard.Enabled {
		t.Fatalf("bad telegram config: %+v", tg)
	}
}
