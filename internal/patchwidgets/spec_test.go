package patchwidgets

import (
	"encoding/json"
	"testing"
)

func TestAskSpecValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    AskSpec
		wantErr bool
	}{
		{"ok", AskSpec{Input: "Name?"}, false},
		{"missing input", AskSpec{}, true},
		{"negative timeout", AskSpec{Input: "x", TimeoutSeconds: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err=%v want=%v", err, tc.wantErr)
			}
		})
	}
}

func TestPickSpecValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    PickSpec
		wantErr bool
	}{
		{"ok", PickSpec{Input: "x", Choices: []PickChoice{{Value: "a"}}}, false},
		{"empty choices", PickSpec{Input: "x"}, true},
		{"empty value", PickSpec{Input: "x", Choices: []PickChoice{{Label: "a"}}}, true},
		{"missing input", PickSpec{Choices: []PickChoice{{Value: "a"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err=%v want=%v", err, tc.wantErr)
			}
		})
	}
}

func TestConfirmSpecValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    ConfirmSpec
		wantErr bool
	}{
		{"ok", ConfirmSpec{Input: "?"}, false},
		{"yes", ConfirmSpec{Input: "?", Default: "yes"}, false},
		{"no", ConfirmSpec{Input: "?", Default: "no"}, false},
		{"bad default", ConfirmSpec{Input: "?", Default: "maybe"}, true},
		{"missing input", ConfirmSpec{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("got err=%v want=%v", err, tc.wantErr)
			}
		})
	}
}

func TestSpecJSONRoundTrip(t *testing.T) {
	in := PickSpec{
		Input:   "Pick",
		Choices: []PickChoice{{Value: "a", Label: "Apple"}, {Value: "b"}},
		Default: "b",
		Filter:  true,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out PickSpec
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Default != "b" || len(out.Choices) != 2 || out.Choices[0].Label != "Apple" {
		t.Fatalf("round trip lost data: %+v", out)
	}
}
