package fakellm

import (
	"context"
	"sync"

	"github.com/weatherjean/shell3/internal/llm"
)

// BlockingClient is an llm.Streamer whose Stream blocks until ctx is
// cancelled, closing Started on its first call.
type BlockingClient struct {
	Started chan struct{}
	once    sync.Once
}

func NewBlocking() *BlockingClient {
	return &BlockingClient{Started: make(chan struct{})}
}

func (b *BlockingClient) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamEvent)) error {
	b.once.Do(func() { close(b.Started) })
	<-ctx.Done()
	return ctx.Err()
}
