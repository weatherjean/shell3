package luacfg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaHTTPGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("BODY"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, dir, ".env", "URL="+srv.URL+"\n")
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
local r, err = shell3.http.get(shell3.env.secret("URL"), { timeout = 5 })
ok = (err == nil) and (r.status == 201) and (r.body == "BODY")
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.L.GetGlobal("ok") != lua.LTrue {
		t.Fatalf("http.get failed")
	}
}

// TestLuaHTTPHonorsToolContext proves httpExec builds the request from the VM's
// tool context (L.Context), so a pre-cancelled context aborts the request
// immediately rather than waiting for the server or the client timeout.
func TestLuaHTTPHonorsToolContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Second) // would block far past the test if not cancelled
		w.WriteHeader(200)
	}))
	defer srv.Close()

	L := lua.NewState()
	defer L.Close()
	c := &LoadedConfig{L: L}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	L.SetContext(ctx)

	L.Push(lua.LString(srv.URL))
	opts := L.NewTable()
	opts.RawSetString("timeout", lua.LNumber(120))
	L.Push(opts)

	start := time.Now()
	n := c.luaHTTPGet(L)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("httpExec ignored cancelled tool context (took %v)", elapsed)
	}
	if n != 2 {
		t.Fatalf("expected 2 return values, got %d", n)
	}
	if L.Get(-1) == lua.LNil {
		t.Fatalf("expected an error string for cancelled request, got nil")
	}
}

func TestLuaHTTPMultiValuedHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, dir, ".env", "URL="+srv.URL+"\n")
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
local r, err = shell3.http.get(shell3.env.secret("URL"), { timeout = 5 })
cookie = r.headers["set-cookie"]
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := c.L.GetGlobal("cookie").String()
	if got != "a=1, b=2" {
		t.Fatalf("multi-valued header not joined: %q", got)
	}
}
