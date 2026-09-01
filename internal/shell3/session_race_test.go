package shell3

import (
	"context"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestSession_ConcurrentReadsRaceTurn(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{
			LLM:   block,
			Agent: "code",

			Profile: chat.AgentProfile{
				SystemPrompt: "you are a test agent",
				Tools:        []llm.ToolDefinition{{Name: "bash"}},
			},
		}
	})
	s, err := rt.Session(SessionOpts{WorkDir: rt.workDir})
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.MessageCount()
					_ = s.Snapshot()
				}
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	turn := s.Send(ctx, "hello") // turn goroutine appends the user message, then blocks in Stream
	<-block.Started

	cancel()
	for range turn {
	}
	close(stop)
	wg.Wait()
}
