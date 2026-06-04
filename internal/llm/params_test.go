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
	base := RequestParams{ReasoningEffort: "medium", MaxTokens: 8000}
	override := RequestParams{ReasoningEffort: "high"}
	got := base.Merge(override)
	if got.ReasoningEffort != "high" {
		t.Fatalf("override lost: %+v", got)
	}
	if got.MaxTokens != 8000 {
		t.Fatalf("base max_tokens lost: %+v", got)
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

func TestRequestParamsSetByNameTemperature(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("temperature", "0.5"); err != nil {
			t.Fatalf("0.5 should be valid: %v", err)
		}
		if p.Temperature == nil || *p.Temperature != 0.5 {
			t.Fatalf("temperature not set to 0.5: %+v", p.Temperature)
		}
	})
	t.Run("trailing garbage rejected", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("temperature", "0.5abc"); err == nil {
			t.Fatal(`"0.5abc" must be rejected`)
		}
		if p.Temperature != nil {
			t.Fatalf("temperature must not be set on invalid input: %+v", p.Temperature)
		}
	})
	t.Run("extra value rejected", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("temperature", "0.5 1.2"); err == nil {
			t.Fatal(`"0.5 1.2" must be rejected`)
		}
	})
	t.Run("fully unparseable rejected", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("temperature", "abc"); err == nil {
			t.Fatal(`"abc" must be rejected`)
		}
	})
}

func TestRequestParamsSetByNameMaxTokens(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("max_tokens", "100"); err != nil {
			t.Fatalf("100 should be valid: %v", err)
		}
		if p.MaxTokens != 100 {
			t.Fatalf("max_tokens not set to 100: %d", p.MaxTokens)
		}
	})
	t.Run("float truncation rejected", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("max_tokens", "12.9"); err == nil {
			t.Fatal(`"12.9" must be rejected for max_tokens`)
		}
		if p.MaxTokens != 0 {
			t.Fatalf("max_tokens must not be set on invalid input: %d", p.MaxTokens)
		}
	})
	t.Run("trailing garbage rejected", func(t *testing.T) {
		var p RequestParams
		if err := p.SetByName("max_tokens", "100x"); err == nil {
			t.Fatal(`"100x" must be rejected`)
		}
	})
}
