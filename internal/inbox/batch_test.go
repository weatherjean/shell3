package inbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBatchProgressWriteFailureIsRetryable(t *testing.T) {
	s := Store{Root: t.TempDir()}
	r, err := s.Notify(Request{To: "main", Source: "test", Event: "done", Body: strings.Repeat("x", batchBodyBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PrepareBatch()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(s.deliveryProgressPath(r.ID))
	if err := os.WriteFile(path, []byte("block directory creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err == nil {
		t.Fatal("ignored failed progress write")
	}
	b.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	retry, err := s.PrepareBatch()
	if err != nil || retry == nil {
		t.Fatalf("retry: %v", err)
	}
	defer retry.Close()
	if retry.parts[0].Offset != 0 {
		t.Fatal("skipped unsaved chunk")
	}
	if err := retry.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchLeaseChunksAndClearing(t *testing.T) {
	s := Store{Root: t.TempDir()}
	body := strings.Repeat("界", batchBodyBytes)
	r, err := s.Notify(Request{To: "main", Source: "test", Event: "done", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for i := 0; i < 4; i++ {
		b, err := s.PrepareBatch()
		if err != nil || b == nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		other, err := s.PrepareBatch()
		if err != nil || other != nil {
			t.Fatal("concurrent consumer acquired batch")
		}
		part := b.parts[0]
		if !utf8.ValidString(part.Body) || len(part.Body) > batchBodyBytes {
			t.Fatal("invalid chunk")
		}
		got.WriteString(part.Body)
		if err := b.Commit(); err != nil {
			t.Fatal(err)
		}
		b.Close()
		n, err := s.Read("main", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if part.End < part.Total && n.Status != StatusNew {
			t.Fatal("partial notice cleared")
		}
		if part.End == part.Total {
			if n.Status != StatusArchived {
				t.Fatal("complete notice pending")
			}
			break
		}
	}
	if got.String() != body {
		t.Fatal("notice bytes lost or duplicated")
	}
	if b, err := s.PrepareBatch(); err != nil || b != nil {
		t.Fatal("archived notice redelivered")
	}
}

func TestBatchFailureLeavesUnreadAndRejectsChangedBody(t *testing.T) {
	s := Store{Root: t.TempDir()}
	r, err := s.Notify(Request{To: "main", Source: "test", Event: "done", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PrepareBatch()
	if err != nil {
		t.Fatal(err)
	}
	first := b.Prompt()
	b.Close()
	b, err = s.PrepareBatch()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.parts[0].Offset != 0 || b.Prompt() == first {
		t.Fatal("failed read advanced or reused delivery identity")
	}
	n, err := s.Read("main", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	n.Message.Body = "changed!"
	if err := s.persist(n.Message); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err == nil {
		t.Fatal("cleared different body")
	}
	if _, err := os.Stat(s.deliveryProgressPath(r.ID)); !os.IsNotExist(err) {
		t.Fatal("saved progress for failed read")
	}
}

func TestBatchCoalescesNoticesAndIgnoresCLIReadProgress(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for range 3 {
		r, err := s.Notify(Request{To: "main", Source: "test", Event: "done", Body: "body"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordRead("main", r.ID, 0, 4, 4); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.PrepareBatch()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if len(b.parts) != 3 {
		t.Fatal("notices not coalesced")
	}
	for _, p := range b.parts {
		if p.Offset != 0 || p.Body != "body" {
			t.Fatal("CLI read treated as saved history")
		}
	}
}
