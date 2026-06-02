package luacfg

import (
	"testing"
	"time"
)

func TestLoadWeb(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.web({
  host = "0.0.0.0",
  port = 9000,
  password = "hunter2",
  cookie_ttl = "24h",
  allowed_origins = { "https://app.example.com" },
})
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := c.Web
	if !w.Set {
		t.Fatal("Web.Set = false, want true")
	}
	if w.Host != "0.0.0.0" || w.Port != 9000 || w.Password != "hunter2" {
		t.Fatalf("bad web config: %+v", w)
	}
	if w.CookieTTL != 24*time.Hour {
		t.Fatalf("cookie_ttl = %v, want 24h", w.CookieTTL)
	}
	if len(w.AllowedOrigins) != 1 || w.AllowedOrigins[0] != "https://app.example.com" {
		t.Fatalf("allowed_origins = %v", w.AllowedOrigins)
	}
}

func TestLoadWebOmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Web.Set {
		t.Fatal("Web.Set = true with no shell3.web block")
	}
}

func TestLoadWebBadTTL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua",
		`shell3.web({ cookie_ttl = "not-a-duration" })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), "cookie_ttl") {
		t.Fatalf("want cookie_ttl parse error, got %v", err)
	}
}

func TestLoadWebUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.web({ nope = 1 })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want strict-key failure, got %v", err)
	}
}
