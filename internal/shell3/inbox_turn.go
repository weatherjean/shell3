package shell3

import (
	"context"
	"errors"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/llm"
)

// SendInbox consumes a leased batch through the ordinary turn pipeline. A
// successful model response alone is insufficient: verify saved input before
// advancing read progress or clearing notices. The batch is always released.
func (s *Session) SendInbox(ctx context.Context, batch *inbox.Batch) <-chan Event {
	out := make(chan Event)
	store := sessionStore(s)
	if store == nil {
		batch.Close()
		return closedEvents(errors.New("inbox: automatic delivery requires durable conversation history"))
	}
	prompt := batch.Prompt()
	events := s.Send(ctx, prompt)
	go func() {
		defer close(out)
		defer batch.Close()
		success := false
		for ev := range events {
			if ev.Kind == Done {
				success = true
			}
			if ev.Kind == Error {
				success = false
			}
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}
		if !success || ctx.Err() != nil {
			return
		}
		messages, err := store.LoadMessages(s.ID())
		found := false
		for _, m := range messages {
			if m.Role == llm.RoleUser && m.Content == prompt {
				found = true
				break
			}
		}
		if err == nil && !found {
			err = errors.New("inbox: notice input was not saved to conversation history; left pending")
		}
		if err == nil {
			err = batch.Commit()
		}
		if err == nil {
			for _, id := range batch.CompletedJobs() {
				_, _ = s.PollJob(id, 0)
			}
		}
		if err != nil {
			select {
			case out <- Event{Kind: Error, Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out
}
