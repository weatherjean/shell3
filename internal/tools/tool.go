// Package tools implements LLM-callable tools.
package tools

import (
	"context"

	"github.com/weatherjean/shell3/internal/llm"
)

// Tool is an LLM-callable function with a definition and executor.
type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, params map[string]any) (string, error)
}
