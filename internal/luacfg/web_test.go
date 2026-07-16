package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// loadWebConfig writes src as a shell3.lua in a temp dir and loads it with a
// minimal model+agent preamble so the config is otherwise valid.
func loadWebConfig(t *testing.T, src string) (*LoadedConfig, error) {
	t.Helper()
	dir := t.TempDir()
	full := `
shell3.model("m", { base_url = "http://127.0.0.1:1", api_key = "k", model = "m" })
shell3.agent{ name = "code", prompt = "p" }
` + src
	path := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestWebBlockParsed(t *testing.T) {
	c, err := loadWebConfig(t, `
shell3.web({
  addr   = "127.0.0.1:8787",
  secret = "s3",
  url    = "https://x.example",
  tunnel = "cloudflared tunnel --url http://{addr}",
})
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	w := c.Web()
	if w.Addr != "127.0.0.1:8787" || w.Secret != "s3" || w.URL != "https://x.example" ||
		w.Tunnel != "cloudflared tunnel --url http://{addr}" {
		t.Fatalf("bad web config: %+v", w)
	}
}

func TestWebBlockAbsentIsZero(t *testing.T) {
	c, err := loadWebConfig(t, "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w := c.Web(); w != (WebConfig{}) {
		t.Fatalf("want zero value, got %+v", w)
	}
}

func TestWebBlockUnknownKeyFails(t *testing.T) {
	_, err := loadWebConfig(t, `shell3.web({ addrr = "typo" })`)
	if err == nil {
		t.Fatal("unknown key must fail the load")
	}
}
