package wrk

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

func testConfig(t *testing.T) *lispconfig.Config {
	t.Helper()
	cfg, err := lispconfig.Parse("shell3.lisp", []byte(`(shell3
  (version 1)
  (runner fake
    (command "/usr/bin/fake-agent")
    (arguments workdir result-file)
    (result (file result-file)))
  (agent researcher (using fake))
  (agent builder (using fake)))`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const validWorkflow = `(task "ship-auth"
  (root ".")
  (parallel 2)

  (agent inspect-api
    (using researcher)
    (access read)
    (prompt """
Inspect the API and write api.md.
""")
    (accept (file "api.md")))

  (loop implement
    (using builder)
    (after inspect-api)
    (access write)
    (max 8)
    (prompt "Implement the smallest unfinished part.")
    (until (sh """
go test ./...
"""))))
`

func TestParseWorkflow(t *testing.T) {
	d, err := Parse("auth.wrk.lisp", []byte(validWorkflow), testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "ship-auth" || d.Parallel != 2 || len(d.Nodes) != 2 {
		t.Fatalf("definition = %+v", d)
	}
	loop := d.Nodes[1]
	if loop.Kind != LoopNode || loop.Using != "builder" || loop.Max != 8 || loop.Until.Kind != "sh" {
		t.Fatalf("loop = %+v", loop)
	}
}

func TestParseRejectsBadWorkflow(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "unknown agent", src: `(task x (agent a (using nope) (prompt "x")))`, want: `uses unknown agent "nope"`},
		{name: "unknown dependency", src: `(task x (agent a (using builder) (after missing) (prompt "x")))`, want: `depends on unknown node "missing"`},
		{name: "cycle", src: `(task x (agent a (using builder) (after b) (prompt "x")) (agent b (using builder) (after a) (prompt "x")))`, want: `dependency cycle`},
		{name: "bad loop", src: `(task x (loop a (using builder) (prompt "x")))`, want: `requires using, prompt, max, and until`},
		{name: "removed fresh field", src: `(task x (loop a (using builder) (fresh) (max 2) (prompt "x") (until (sh "true"))))`, want: `unknown loop field "fresh"`},
		{name: "wrong field", src: `(task x (command a (using builder) (run "true")))`, want: `cannot use "using"`},
		{name: "unsupported isolated access", src: `(task x (command a (access isolated) (run "true")))`, want: `must be one of [read write]`},
		{name: "duplicate node", src: `(task x (command a (run "true")) (command a (run "true")))`, want: `duplicate node "a"`},
		{name: "unsafe artifact", src: `(task x (agent a (using builder) (prompt "x") (accept (file "../secret"))))`, want: `artifact path must be relative`},
		{name: "unsafe task name", src: `(task "../x" (command a (run "true")))`, want: `invalid task name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("bad.wrk.lisp", []byte(tt.src), testConfig(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
