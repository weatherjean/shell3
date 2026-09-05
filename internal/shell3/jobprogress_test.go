package shell3

import (
	"testing"
	"time"
)

func TestJobSink(t *testing.T) {
	ring := newRingBuffer(1024)
	sink := &jobSink{ring: ring}

	if _, err := sink.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := sink.Write([]byte(" world")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := sink.String(); got != "hello world" {
		t.Errorf("String() = %q, want %q", got, "hello world")
	}
}

func TestJobCompletionsNonNil(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if rt.JobCompletions() == nil {
		t.Fatal("JobCompletions() returned nil channel")
	}
}

func TestEmitJobCompletionNeverBlocks(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	done := make(chan struct{})
	go func() {
		for i := 0; i <= defaultMaxConcurrent; i++ {
			rt.emitJobCompletion()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitJobCompletion blocked on a full channel")
	}
}

func TestJobCompletionIntegration(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("done"))

	sess, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	_, err = rt.jobs.startCommand(sess, "echo hello", t.TempDir(), []string{"echo", "hello"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	select {
	case <-rt.JobCompletions():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job completion")
	}
}
