package patchwidgets

import (
	"encoding/json"
	"testing"
)

func TestResultJSON(t *testing.T) {
	idx := 2
	r := Result{OK: true, Value: "main", Index: &idx, Reason: ReasonOK}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ok":true,"value":"main","index":2,"reason":"ok"}`
	if string(data) != want {
		t.Fatalf("got %s want %s", data, want)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want int
	}{
		{"ok string", Result{OK: true, Value: "x", Reason: ReasonOK}, 0},
		{"ok yes", Result{OK: true, Value: true, Reason: ReasonOK}, 0},
		{"ok no", Result{OK: true, Value: false, Reason: ReasonOK}, 1},
		{"timeout", Result{Reason: ReasonTimeout}, 2},
		{"cancel", Result{Reason: ReasonCancel}, 130},
		{"eof", Result{Reason: ReasonEOF}, 130},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.ExitCode(); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
