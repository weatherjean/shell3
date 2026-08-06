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
  workdir: /tmp/agent
  chat_id: "123456789"
`,
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	tg := rt.Telegram()
	if tg.WorkDir != "/tmp/agent" || tg.ChatID != "123456789" {
		t.Fatalf("bad telegram config: %+v", tg)
	}
}
