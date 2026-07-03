package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// pumpJobs forwards background-job progress events from the runtime's JobEvents
// bus to the ACP client as synthetic per-job tool-call cards.
//
// Each job gets its own tool-call card whose toolCallId IS the job id (e.g.
// "bg1", "sub1"). The parent task tool-call remains untouched and completes
// immediately — these cards are independent.
//
// conn is read ONCE under a.mu at the start (matching pump's discipline) and
// used for the goroutine's lifetime.
func (a *acpAgent) pumpJobs(ctx context.Context) {
	a.pumpJobsFrom(ctx, a.rt.JobEvents())
}

// pumpJobsFrom is the testable core of pumpJobs. It reads JobProgress events
// from ev and emits synthetic StartToolCall / UpdateToolCall notifications.
// Extracted as a named method so tests can pass their own channel without
// touching the runtime's unexported emitJob.
//
// Lifecycle:
//   - StartToolCall (in_progress)   — emitted on first event for a job
//   - UpdateToolCall (in_progress)  — emitted per non-empty Chunk
//   - UpdateToolCall (completed)    — emitted on the terminal Done event
//
// Events whose Parent is not registered in this front-end (sessionByName
// returns nil) are silently skipped — they belong to another connection or
// a child session.
func (a *acpAgent) pumpJobsFrom(ctx context.Context, ev <-chan shell3.JobProgress) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return
	}

	started := map[string]bool{} // job ids that have already received StartToolCall
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-ev:
			if !ok {
				return // channel closed
			}
			s := a.sessionByName(p.Parent)
			if s == nil {
				continue // parent not owned by this front-end — skip
			}
			tcID := acpsdk.ToolCallId(p.JobID)

			// Emit StartToolCall on first event for this job.
			if !started[p.JobID] {
				started[p.JobID] = true
				title := p.Title
				if p.Kind == shell3.JobSubagent {
					title = "subagent: " + p.Title
				}
				_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
					SessionId: acpsdk.SessionId(s.id),
					Update: acpsdk.StartToolCall(tcID, title,
						acpsdk.WithStartKind(acpsdk.ToolKindOther),
						acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress)),
				})
			}

			switch {
			case p.Done:
				// Terminal event: mark completed, include summary for subagent jobs.
				// This emit is best-effort (drop-on-full like all progress events): if
				// dropped under sustained backpressure the card stays in_progress and its
				// started entry lingers — this is intentional; the ring buffer and JobInfo
				// remain authoritative.
				opts := []acpsdk.ToolCallUpdateOpt{
					acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
				}
				if p.Summary != "" {
					opts = append(opts, acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
						acpsdk.ToolContent(acpsdk.TextBlock(p.Summary)),
					}))
				}
				_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
					SessionId: acpsdk.SessionId(s.id),
					Update:    acpsdk.UpdateToolCall(tcID, opts...),
				})
				delete(started, p.JobID)

			case p.Chunk != "":
				// Incremental chunk: send the delta (ACP convention — client merges).
				_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
					SessionId: acpsdk.SessionId(s.id),
					Update: acpsdk.UpdateToolCall(tcID,
						acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusInProgress),
						acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
							acpsdk.ToolContent(acpsdk.TextBlock(p.Chunk)),
						})),
				})
			}
		}
	}
}
