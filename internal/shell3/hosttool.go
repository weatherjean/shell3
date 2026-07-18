package shell3

import (
	"context"
	"errors"
	"fmt"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// HostTool is a Go-implemented tool the host registers on a Session so the
// model can call it (e.g. media's image_generate).
type HostTool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema for the arguments object
	Handler     func(ctx context.Context, argsJSON string) (string, error)
}

// RegisterHostTool adds a host tool to this session's schema and dispatch. Call
// before the first turn; it mutates session config and is not safe to call
// concurrently with a turn. Multiple registrations compose.
func (s *Session) RegisterHostTool(t HostTool) error {
	if t.Name == "" || t.Handler == nil {
		return errors.New("shell3: host tool requires a Name and Handler")
	}
	// Guard the cfg mutations against the dashboard's concurrent Snapshot read
	// (reads Personality.Tools under s.mu). Between turns by contract, enforced
	// by the busy check below.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	s.cfg.Personality.Tools = append(s.cfg.Personality.Tools, llm.ToolDefinition{
		Name: t.Name, Description: t.Description, Parameters: t.Parameters,
	})
	if s.cfg.HostToolNames == nil {
		s.cfg.HostToolNames = map[string]bool{}
	}
	s.cfg.HostToolNames[t.Name] = true
	prev := s.cfg.HostTool
	name, handler := t.Name, t.Handler
	s.cfg.HostTool = func(ctx context.Context, called, argsJSON string) (string, error) {
		if called == name {
			return handler(ctx, argsJSON)
		}
		if prev != nil {
			return prev(ctx, called, argsJSON)
		}
		return "", fmt.Errorf("%w: %q", chat.ErrHostToolNotFound, called)
	}
	return nil
}
