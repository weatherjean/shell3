package llm_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestNewClient_Smoke(t *testing.T) {
	c := llm.NewClient("http://localhost:11434/v1", "", "llama3.2")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}
