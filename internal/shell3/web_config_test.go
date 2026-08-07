package shell3_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRuntime_WebConfig(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, map[string]string{
		"shell3.yaml": baseYAML + `web:
  workdir: /tmp/agent
  addr: "127.0.0.1:8765"
  url: "https://h.ts.net/"
`,
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	web := rt.Web()
	if web.WorkDir != "/tmp/agent" || web.Addr != "127.0.0.1:8765" || web.URL != "https://h.ts.net/" {
		t.Fatalf("bad web config: %+v", web)
	}
}
