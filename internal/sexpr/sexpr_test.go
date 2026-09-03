package sexpr

import (
	"strings"
	"testing"
)

func TestParseValuesAndPositions(t *testing.T) {
	forms, err := Parse("sample.wrk.lisp", []byte("; heading\n(task \"demo\" (parallel 3) enabled)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(forms) != 1 {
		t.Fatalf("forms = %d, want 1", len(forms))
	}
	task := forms[0]
	if head, ok := task.Head(); !ok || head != "task" {
		t.Fatalf("head = %q, %v", head, ok)
	}
	if task.Pos.Filename != "sample.wrk.lisp" || task.Pos.Line != 2 || task.Pos.Column != 1 {
		t.Fatalf("position = %+v", task.Pos)
	}
	if got := task.Children[1]; got.Kind != String || got.Value != "demo" {
		t.Fatalf("task name = %+v", got)
	}
	parallel := task.Children[2]
	if got := parallel.Children[1]; got.Kind != Number || got.Integer != 3 {
		t.Fatalf("parallel value = %+v", got)
	}
	if got := task.Children[3]; got.Kind != Symbol || got.Value != "enabled" {
		t.Fatalf("symbol = %+v", got)
	}
}

func TestParseStringEscapes(t *testing.T) {
	forms, err := Parse("strings.lisp", []byte(`(value "line\n\"quoted\"")`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := forms[0].Children[1].Value, "line\n\"quoted\""; got != want {
		t.Fatalf("string = %q, want %q", got, want)
	}
}

func TestParseRawString(t *testing.T) {
	src := `(prompt """
first line
last line
""")
`
	forms, err := Parse("prompt.wrk.lisp", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "\nfirst line\nlast line\n"
	if got := forms[0].Children[1]; got.Kind != String || got.Value != want {
		t.Fatalf("raw string = %#v, want %q", got, want)
	}
}

func TestParseRejectsBrokenStructure(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "unexpected close", src: ")", want: "broken.lisp:1:1: unexpected )"},
		{name: "open list", src: "(task", want: "broken.lisp:1:1: list is not closed"},
		{name: "open string", src: `(task "no)`, want: "broken.lisp:1:7"},
		{name: "open raw string", src: "(prompt \"\"\"\nbody\n)", want: "broken.lisp:1:9: raw string is not closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("broken.lisp", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseMultipleForms(t *testing.T) {
	forms, err := Parse("many.lisp", []byte("(define one 1)\n(define two 2)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(forms) != 2 {
		t.Fatalf("forms = %d, want 2", len(forms))
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"builder", "research-api", "agent_2"} {
		if !ValidName(name) {
			t.Errorf("ValidName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "2agent", "../agent", "agent.name", "agent/name"} {
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true", name)
		}
	}
}
