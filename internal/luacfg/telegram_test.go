package luacfg

import (
	"strings"
	"testing"
)

func TestLoadTelegram(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.telegram({
  token = "bot-token",
  chat_id = "123456789",
  dashboard = { enabled = true, addr = "127.0.0.1:8765", url = "https://h.ts.net/" },
})
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tg := c.Telegram()
	if tg.Token != "bot-token" || tg.ChatID != "123456789" {
		t.Fatalf("bad telegram: %+v", tg)
	}
	if !tg.Dashboard.Enabled || tg.Dashboard.Addr != "127.0.0.1:8765" || tg.Dashboard.URL != "https://h.ts.net/" {
		t.Fatalf("bad dashboard: %+v", tg.Dashboard)
	}
}

func TestLoadTelegramUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.telegram({ token="x", chat_id="1", nope=true })
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("wrong error: %v", err)
	}
}
