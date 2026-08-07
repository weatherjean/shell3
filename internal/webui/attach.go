//go:build unix

package webui

// Reconnect-and-replay. A turn used to borrow its lifetime from the HTTP
// request that started it — reasonable at a desk, fatal on a phone, where
// locking the screen kills the connection within seconds and took the turn
// with it. Now the turn writes into a turnBroker: every protocol chunk is
// buffered for the turn's lifetime and fanned out to however many readers are
// attached. The original POST response is just the first reader; a client
// that comes back re-attaches with GET /api/chat/stream?thread=… and gets the
// whole turn replayed, then followed live. The AI SDK's resumeStream() speaks
// exactly this contract (204 = nothing running).
//
// What keeps a detached turn from running invisible forever: /api/stop still
// cancels it, the Status view still shows the slot taken, and the buffer is
// dropped the moment the turn ends.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// turnBroker buffers one turn's stream chunks and fans them out to attached
// readers. The buffer is authoritative: a reader is an index into it, so a
// slow reader never blocks the turn and a late reader misses nothing.
type turnBroker struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte // marshaled protocol chunks, in emit order
	closed bool
}

func newTurnBroker() *turnBroker {
	b := &turnBroker{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// writeChunk implements chunkSink: buffer the chunk, wake the readers. It
// never fails — the broker outlives any one reader by design.
func (b *turnBroker) writeChunk(payload []byte) error {
	p := make([]byte, len(payload))
	copy(p, payload)
	b.mu.Lock()
	b.chunks = append(b.chunks, p)
	b.mu.Unlock()
	b.cond.Broadcast()
	return nil
}

// done implements chunkSink: the turn is over. Idempotent.
func (b *turnBroker) done() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

// serve replays the buffer from the start onto w, then follows the turn live,
// returning when the turn ends (after writing the [DONE] marker) or when ctx
// — the reader's own request — is cancelled. SSE headers are the caller's.
func (b *turnBroker) serve(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) {
	// cond.Wait cannot watch a context, so a cancelled reader is woken by
	// broadcasting from the context's own callback.
	stop := context.AfterFunc(ctx, func() { b.cond.Broadcast() })
	defer stop()

	next := 0
	for {
		b.mu.Lock()
		for next == len(b.chunks) && !b.closed && ctx.Err() == nil {
			b.cond.Wait()
		}
		pending := b.chunks[next:]
		next = len(b.chunks)
		closed := b.closed
		b.mu.Unlock()

		if ctx.Err() != nil {
			return
		}
		for _, p := range pending {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", p); err != nil {
				return // this reader is gone; the turn does not care
			}
		}
		flusher.Flush()
		if closed {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
}

// setAttachable publishes the running turn's broker under its thread id.
func (s *Server) setAttachable(thread string, b *turnBroker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachable, s.attachableThread = b, thread
}

// clearAttachable retires the broker — only if it is still the current one,
// so a slow goroutine cannot clear its successor's.
func (s *Server) clearAttachable(b *turnBroker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attachable == b {
		s.attachable, s.attachableThread = nil, ""
	}
}

// attachableFor is the running turn's broker, when that turn belongs to
// thread; nil otherwise.
func (s *Server) attachableFor(thread string) *turnBroker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attachable == nil || s.attachableThread != thread {
		return nil
	}
	return s.attachable
}

// handleChatAttach is GET /api/chat/stream?thread=…: re-attach to the running
// turn. 204 when there is nothing to attach to — the AI SDK reads that as
// "the response already completed" and moves on.
func (s *Server) handleChatAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	broker := s.attachableFor(r.URL.Query().Get("thread"))
	if broker == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	writeSSEHeaders(w, flusher)
	broker.serve(r.Context(), w, flusher)
}
