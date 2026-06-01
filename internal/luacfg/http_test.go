package luacfg

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
