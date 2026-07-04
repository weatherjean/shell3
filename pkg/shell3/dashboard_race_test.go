package shell3

import (
	"context"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestSession_DashboardReadsRaceTurn pins P0: a front-end polls
// History() and Snapshot() from net/http goroutines concurrently with a running
// turn on the same Session. Before the fix the turn goroutine's append to
// chat.Session.messages raced the dashboard's slice copy, and Snapshot's
// unlocked cfg reads raced the between-turns cfg writers. The reader goroutines
// here hammer both endpoints across the whole turn lifecycle — the user-message
// append at turn start (before Started) and the terminal append after cancel —
// so the race window is covered on both ends. Must be clean under -race.
func TestSession_DashboardReadsRaceTurn(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{
			LLM:        block,
			ModeLabel:  "code",
			AgentNames: []string{"code"},
			Personality: persona.Persona{
				SystemPrompt: "you are a test agent",
				Tools:        []llm.ToolDefinition{{Name: "bash"}},
			},
		}
	})
	s, err := rt.Session(SessionOpts{Name: "tg:1", WorkDir: rt.workDir})
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
					_ = s.History()
					_ = s.Snapshot()
				}
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	turn := s.Send(ctx, "hello") // turn goroutine appends the user message, then blocks in Stream
	<-block.Started

	cancel()
	for range turn { // drain until the turn's terminal append + channel close
	}
	close(stop)
	wg.Wait()
}
