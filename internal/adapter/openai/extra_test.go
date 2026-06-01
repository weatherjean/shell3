package openai

import "testing"

func TestSetExtra(t *testing.T) {
	c := NewClient("https://x/v1", "k", "m")
	c.SetExtra(map[string]any{"verbosity": "high"})
	if c.extra["verbosity"] != "high" {
		t.Fatalf("extra not stored: %+v", c.extra)
	}
}
