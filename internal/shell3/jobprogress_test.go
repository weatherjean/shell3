package shell3

import (
	"testing"
	"time"
)

// TestJobSink verifies that jobSink tees written bytes to both the ring buffer
// and the emit callback.
func TestJobSink(t *testing.T) {
	var chunks []string
	ring := newRingBuffer(1024)
	sink := &jobSink{
		ring: ring,
		emit: func(c string) { chunks = append(chunks, c) },
	}

	if _, err := sink.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := sink.Write([]byte(" world")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Ring buffer should have both bytes via String().
	if got := sink.String(); got != "hello world" {
		t.Errorf("String() = %q, want %q", got, "hello world")
	}
	// Emit callback should have received both chunk strings.
	if len(chunks) != 2 || chunks[0] != "hello" || chunks[1] != " world" {
		t.Errorf("chunks = %v, want [\"hello\" \" world\"]", chunks)
	}
}

// TestJobEventsNonNil verifies that rt.JobEvents() returns a non-nil channel.
func TestJobEventsNonNil(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if rt.JobEvents() == nil {
		t.Fatal("JobEvents() returned nil channel")
	}
}

// TestEmitJobNeverBlocks verifies that emitJob never blocks when the buffer is
// full (buffer is 256; we fill 256+1 = 257 events).
func TestEmitJobNeverBlocks(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	done := make(chan struct{})
	go func() {
		for i := 0; i <= 256; i++ {
			rt.emitJob(JobProgress{
				JobID: "bg1", Parent: "s1", Kind: JobCommand,
				Title: "test", Chunk: "x",
			})
		}
		close(done)
	}()
	select {
	case <-done:
		// passed
	case <-time.After(2 * time.Second):
		t.Fatal("emitJob blocked on a full channel (deadlock)")
	}
}

// TestJobProgressIntegration runs a fast bash_bg command and drains
// JobEvents, asserting at least one chunk event and exactly one Done event
// arrive with JobID and Parent populated.
func TestJobProgressIntegration(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("done"))

	sess, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	id, err := rt.jobs.startCommand(sess, "echo hello", t.TempDir(), []string{"echo", "hello"}, nil, false)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	var chunks []JobProgress
	var done []JobProgress
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev := <-rt.JobEvents():
			if ev.JobID != id {
				continue
			}
			if ev.Done {
				done = append(done, ev)
				break loop
			}
			chunks = append(chunks, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for Done event (chunks=%d)", len(chunks))
		}
	}

	if len(chunks) < 1 {
		t.Errorf("want ≥1 chunk events, got %d", len(chunks))
	}
	if len(done) != 1 {
		t.Errorf("want exactly 1 Done event, got %d", len(done))
	}
	if d := done[0]; d.JobID == "" || d.Parent == "" {
		t.Errorf("Done event missing JobID or Parent: %+v", d)
	}
}
