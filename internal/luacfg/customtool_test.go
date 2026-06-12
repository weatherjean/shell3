package luacfg

import (
	"path/filepath"
	"strings"
	"testing"
)

const toolHdr = `shell3.model("m", { base_url="http://x", api_key="k", model="id" })` + "\n"

func loadToolCfg(t *testing.T, lua string) *LoadedConfig {
	t.Helper()
	p := writeConfig(t, toolHdr+lua)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestToolCommandFieldsParse(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({
  name="echoer", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={ "msg" } },
  command="echo $msg", secrets={ "TOKEN" }, background=true, timeout=42,
})
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	ct := c.Tools["echoer"]
	if ct.Command != "echo $msg" || ct.Timeout != 42 || !ct.Background {
		t.Fatalf("fields = %+v", ct)
	}
	if len(ct.Secrets) != 1 || ct.Secrets[0] != "TOKEN" {
		t.Fatalf("secrets = %v", ct.Secrets)
	}
}

func TestToolHandlerKeyRejected(t *testing.T) {
	p := writeConfig(t, toolHdr+`
shell3.tool({ name="x", description="d", handler=function() return "" end })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("handler key should be rejected now")
	}
}

func TestToolNoCommandErrors(t *testing.T) {
	p := writeConfig(t, toolHdr+`shell3.tool({ name="x", description="d" })`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("tool without command should error")
	}
}

func TestToolUppercaseParamRejected(t *testing.T) {
	p := writeConfig(t, toolHdr+`
shell3.tool({ name="x", description="d", command="echo hi",
  parameters={ type="object", properties={ Query={ type="string" } } } })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("uppercase param name should be rejected")
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

func TestResolveExportsParamsAndSecrets(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({
  name="search", description="d",
  parameters={ type="object", properties={ query={type="string"}, count={type="integer"} } },
  secrets={ "BRAVE_API_KEY" }, command="echo $query",
})
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	c.Secrets["BRAVE_API_KEY"] = "sekret"
	rc, err := c.ResolveCustomCall("search", `{"query":"foo bar","count":5}`)
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(rc.Env)
	if m["query"] != "foo bar" || m["count"] != "5" || m["BRAVE_API_KEY"] != "sekret" {
		t.Fatalf("env = %v", m)
	}
	if rc.Command != "echo $query" {
		t.Fatalf("command = %q", rc.Command)
	}
}

func TestResolveMissingSecretErrors(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({ name="x", description="d", command="echo hi", secrets={ "NOPE" } })
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	if _, err := c.ResolveCustomCall("x", "{}"); err == nil {
		t.Fatal("missing secret should error")
	}
}

func TestResolveEmptySecretErrors(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({ name="x", description="d", command="echo hi", secrets={ "BRAVE_API_KEY" } })
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	c.Secrets["BRAVE_API_KEY"] = "" // present in .env but blank
	if _, err := c.ResolveCustomCall("x", "{}"); err == nil {
		t.Fatal("empty secret should error, not export a blank value")
	}
}

func TestResolveDropsUndeclaredArgs(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({ name="x", description="d", command="echo hi",
  parameters={ type="object", properties={ query={type="string"} } } })
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	// A misbehaving model sends an undeclared key; it must NOT reach the env.
	rc, err := c.ResolveCustomCall("x", `{"query":"ok","PATH":"/evil"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, bad := envMap(rc.Env)["PATH"]; bad {
		t.Fatal("undeclared arg PATH leaked into env")
	}
}
