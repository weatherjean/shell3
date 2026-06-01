package luacfg

import (
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func (c *LoadedConfig) luaHTTPGet(L *lua.LState) int  { return c.httpDo(L, "GET", 1, 2) }
func (c *LoadedConfig) luaHTTPPost(L *lua.LState) int { return c.httpDo(L, "POST", 1, 2) }

// luaHTTPRequest handles shell3.http.request{ url, method, headers, body, timeout, max_bytes }
func (c *LoadedConfig) luaHTTPRequest(L *lua.LState) int {
	o := L.CheckTable(1)
	url := optStr(o, "url")
	method := optStr(o, "method")
	if method == "" {
		method = "GET"
	}
	return c.httpExec(L, method, url, o)
}

func (c *LoadedConfig) httpDo(L *lua.LState, method string, urlIdx, optIdx int) int {
	url := L.CheckString(urlIdx)
	o, _ := L.Get(optIdx).(*lua.LTable)
	if o == nil {
		o = L.NewTable()
	}
	return c.httpExec(L, method, url, o)
}

func (c *LoadedConfig) httpExec(L *lua.LState, method, url string, o *lua.LTable) int {
	timeout := optInt(o, "timeout")
	if timeout <= 0 || timeout > 120 {
		timeout = 30
	}
	maxBytes := optInt(o, "max_bytes")
	if maxBytes <= 0 || maxBytes > 16<<20 {
		maxBytes = 1 << 20
	}
	body := optStr(o, "body")

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("error: " + err.Error()))
		return 2
	}
	if h, ok := o.RawGetString("headers").(*lua.LTable); ok {
		h.ForEach(func(k, v lua.LValue) { req.Header.Set(k.String(), v.String()) })
	}

	var resp *http.Response
	c.withIOUnlock(func() {
		cl := &http.Client{Timeout: time.Duration(timeout) * time.Second}
		resp, err = cl.Do(req)
	})
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("error: " + err.Error()))
		return 2
	}
	defer resp.Body.Close()
	lr := io.LimitReader(resp.Body, int64(maxBytes)+1)
	raw, _ := io.ReadAll(lr)
	truncated := len(raw) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}

	res := L.NewTable()
	res.RawSetString("status", lua.LNumber(resp.StatusCode))
	res.RawSetString("body", lua.LString(string(raw)))
	res.RawSetString("truncated", lua.LBool(truncated))
	hdr := L.NewTable()
	for k := range resp.Header {
		hdr.RawSetString(strings.ToLower(k), lua.LString(resp.Header.Get(k)))
	}
	res.RawSetString("headers", hdr)
	L.Push(res)
	L.Push(lua.LNil)
	return 2
}
