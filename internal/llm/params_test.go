package llm

import "testing"

func TestParamSpecValidate(t *testing.T) {
	spec := ParamSpec{Name: "reasoning_effort", Enum: []string{"low", "medium", "high"}, Default: "medium"}
	if err := spec.Validate("medium"); err != nil {
		t.Fatalf("medium should be valid: %v", err)
	}
	if err := spec.Validate("ultra"); err == nil {
		t.Fatal("ultra must be rejected")
	}
	free := ParamSpec{Name: "temperature"}
	if err := free.Validate("0.7"); err != nil {
		t.Fatalf("freeform spec should accept anything: %v", err)
	}
}

func TestRequestParamsMerge(t *testing.T) {
	base := RequestParams{ReasoningEffort: "medium", Verbosity: "medium"}
	override := RequestParams{ReasoningEffort: "high"}
	got := base.Merge(override)
	if got.ReasoningEffort != "high" {
		t.Fatalf("override lost: %+v", got)
	}
	if got.Verbosity != "medium" {
		t.Fatalf("base verbosity lost: %+v", got)
	}
}

func TestRequestParamsSetByName(t *testing.T) {
	var p RequestParams
	if err := p.SetByName("reasoning_effort", "high"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if p.ReasoningEffort != "high" {
		t.Fatalf("not set: %+v", p)
	}
	if err := p.SetByName("nope", "x"); err == nil {
		t.Fatal("unknown name should fail")
	}
}
