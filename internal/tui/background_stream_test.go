package tui

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// makeStreamModel builds a sized model with a buffered jobEvents channel and a
// fakeCmds holding one initially-running job.
func makeStreamModel(jobID string) (*model, chan shell3.JobProgress, *fakeCmds) {
	fc := &fakeCmds{
		jobs: []shell3.JobInfo{
			{ID: jobID, Cmd: "sleep 60", Kind: shell3.JobCommand, Done: false},
		},
		jobOut: map[string]string{jobID: ""},
	}
	m := sizedWith(closedSend(nil), fc)

	ch := make(chan shell3.JobProgress, 8)
	m.jobEvents = ch
	return m, ch, fc
}

// openBgRaw sets the :background modal open in raw-stdout mode for the given job.
func openBgRaw(m *model, jobID string) {
	m.bg.open = true
	m.bg.viewID = jobID
	m.bg.isTranscript = false
	m.bg.output = ""
	m.bg.rows = nil
}

// TestJobProgressChunkAppendsToOutput verifies that a Chunk event delivered via
// jobProgressMsg ends up in m.bg.output when the modal is open in raw mode on
// that job.
func TestJobProgressChunkAppendsToOutput(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	openBgRaw(m, "sub1")

	m.Update(jobProgressMsg{JobID: "sub1", Parent: "main", Chunk: "hello"})

	if !strings.Contains(m.bg.output, "hello") {
		t.Fatalf("bgOutput should contain 'hello' after chunk event, got %q", m.bg.output)
	}
	// bgRows must be invalidated so the next render recomputes the wrapped lines.
	if m.bg.rows != nil {
		t.Fatal("bgRows should be nil (cache invalidated) after a chunk append")
	}
}

// TestJobProgressChunkIgnoredWhenWrongJob verifies that chunks for a different
// job do not affect the currently-viewed job's output.
func TestJobProgressChunkIgnoredWhenWrongJob(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	openBgRaw(m, "sub1")

	m.Update(jobProgressMsg{JobID: "sub2", Parent: "main", Chunk: "should not appear"})

	if strings.Contains(m.bg.output, "should not appear") {
		t.Fatalf("chunk for sub2 must not affect sub1's bgOutput, got %q", m.bg.output)
	}
}

// TestJobProgressChunkIgnoredWhenTranscriptView verifies that raw chunks are
// not appended into a structured transcript view (bgIsTranscript=true), which
// would corrupt JSONL rendering.
func TestJobProgressChunkIgnoredWhenTranscriptView(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	openBgRaw(m, "sub1")
	m.bg.isTranscript = true
	existing := `{"role":"assistant","content":"existing"}`
	m.bg.output = existing

	m.Update(jobProgressMsg{JobID: "sub1", Parent: "main", Chunk: "raw chunk"})

	if m.bg.output != existing {
		t.Fatalf("transcript view bgOutput should be unchanged, got %q", m.bg.output)
	}
}

// TestJobProgressDoneRefreshesJobList verifies that a Done event causes bgJobs
// and bgCount to be refreshed from cmds.Jobs() so the finished row and pill
// reflect the new state.
func TestJobProgressDoneRefreshesJobList(t *testing.T) {
	fc := &fakeCmds{
		jobs: []shell3.JobInfo{
			// fakeCmds already reports the job as Done (what cmds.Jobs() will return).
			{ID: "sub1", Cmd: "sleep 1", Kind: shell3.JobCommand, Done: true},
		},
		jobOut: map[string]string{"sub1": "finished output"},
	}
	m := sizedWith(closedSend(nil), fc)
	openBgRaw(m, "sub1")
	// Seed stale state — as if the modal captured a snapshot before Done arrived.
	m.bg.jobs = []shell3.JobInfo{{ID: "sub1", Done: false}}
	m.bgCount = 1

	m.Update(jobProgressMsg{JobID: "sub1", Parent: "main", Done: true})

	if m.bgCount != 0 {
		t.Fatalf("bgCount should be 0 after Done event (job finished), got %d", m.bgCount)
	}
	if len(m.bg.jobs) == 0 {
		t.Fatal("bgJobs should be refreshed from cmds on Done")
	}
	if !m.bg.jobs[0].Done {
		t.Fatalf("refreshed job entry should be Done=true, got %+v", m.bg.jobs[0])
	}
}

// TestJobProgressChunkIgnoredWhenModalClosed verifies that chunks do not
// accumulate when the :background modal is not open.
func TestJobProgressChunkIgnoredWhenModalClosed(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	// Open the modal in raw mode for this job (bgViewID=="sub1", bgIsTranscript==false)
	// so that the bgViewID and bgIsTranscript guards are both satisfied — then close it.
	// This ensures the ONLY condition blocking the live-append is m.bg.open; if that
	// guard were removed from production, bgViewID=="sub1" && !bgIsTranscript would
	// remain true and the chunk would land in bgOutput, failing this test.
	openBgRaw(m, "sub1")
	m.bg.open = false

	m.Update(jobProgressMsg{JobID: "sub1", Parent: "main", Chunk: "hidden"})

	if m.bg.output != "" {
		t.Fatalf("chunk must not accumulate when modal is closed, got %q", m.bg.output)
	}
}

// TestJobProgressMultipleChunksAccumulate verifies that successive Chunk events
// are all appended to bgOutput in order.
func TestJobProgressMultipleChunksAccumulate(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	openBgRaw(m, "sub1")

	m.Update(jobProgressMsg{JobID: "sub1", Chunk: "line1\n"})
	m.Update(jobProgressMsg{JobID: "sub1", Chunk: "line2\n"})
	m.Update(jobProgressMsg{JobID: "sub1", Chunk: "line3\n"})

	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(m.bg.output, want) {
			t.Errorf("bgOutput missing %q, got: %q", want, m.bg.output)
		}
	}
}

// TestBgOutputCapBoundsTailPreserved verifies that live-appended chunks beyond
// bgLiveTailCap (64 KB) do not grow m.bg.output unboundedly, and that the most-
// recent content is kept (tail-preserving trim, matching the ring buffer semantics).
func TestBgOutputCapBoundsTailPreserved(t *testing.T) {
	m, _, _ := makeStreamModel("sub1")
	openBgRaw(m, "sub1")

	// Build a single chunk that is slightly larger than the cap (70 KB of 'a' bytes),
	// then send a second trailing marker that must appear in the final output.
	bigChunk := strings.Repeat("a", 70*1024)
	trailer := strings.Repeat("z", 1024) // 1 KB trailer, well within the tail window

	m.Update(jobProgressMsg{JobID: "sub1", Chunk: bigChunk})
	m.Update(jobProgressMsg{JobID: "sub1", Chunk: trailer})

	if len(m.bg.output) > bgLiveTailCap {
		t.Errorf("bgOutput length %d exceeds bgLiveTailCap %d", len(m.bg.output), bgLiveTailCap)
	}
	// Tail must be preserved: the most-recent chunk content should be present.
	if !strings.HasSuffix(m.bg.output, trailer) {
		n := len(m.bg.output)
		start := n - 32
		if start < 0 {
			start = 0
		}
		t.Errorf("bgOutput should end with the trailer chunk (tail preserved); got last 32 bytes: %q",
			m.bg.output[start:])
	}
}
