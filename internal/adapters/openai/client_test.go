package openai_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/adapters/openai"
)

func TestNewClient_Smoke(t *testing.T) {
	c := openai.NewClient("http://localhost:11434/v1", "", "llama3.2")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}
