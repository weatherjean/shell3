//go:build unix

package inbox

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotifyPersistsBeforeUnavailableWake(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	receipt, err := (Store{Root: root, Now: func() time.Time { return now }}).Notify(Request{
		To: "wrk:run-1", Source: "test", Event: "ci.finished", Body: "passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Persisted || receipt.Wake != "unavailable" {
		t.Fatalf("receipt = %+v", receipt)
	}
	path := filepath.Join(root, "inbox", encodeTarget("wrk:run-1"), "new", receipt.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.To != "wrk:run-1" || msg.Trust != "machine" || msg.Body != "passed" || !msg.Created.Equal(now) {
		t.Fatalf("message = %+v", msg)
	}
}

func TestNotifyWakesLiveSocketAfterPersistence(t *testing.T) {
	root := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "shell3-inbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	path := filepath.Join(socketDir, "wake.sock")
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	receipt, err := (Store{Root: root, WakePath: path}).Notify(Request{
		To: "session:abc", Source: "test", Event: "message", Body: "wake up",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Wake != "delivered" {
		t.Fatalf("wake = %q", receipt.Wake)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _, err := listener.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "session:abc" {
		t.Fatalf("wake target = %q", got)
	}
	path = filepath.Join(root, "inbox", encodeTarget("session:abc"), "new", receipt.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("message was not persisted before wake: %v", err)
	}
}

func TestNotifyRejectsInvalidInput(t *testing.T) {
	_, err := (Store{Root: t.TempDir()}).Notify(Request{To: "bad\ntarget", Source: "test", Event: "message"})
	if err == nil {
		t.Fatal("expected invalid target error")
	}
}

func TestClaimAckAndCrashRecovery(t *testing.T) {
	store := Store{Root: t.TempDir()}
	receipt, err := store.Notify(Request{To: "main", Source: "test", Event: "done", Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := store.Claim("main")
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", delivery, ok, err)
	}
	if delivery.Message.ID != receipt.ID || delivery.Message.Body != "first" {
		t.Fatalf("delivery = %+v", delivery.Message)
	}
	if _, ok, err := store.Claim("main"); err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
	if err := store.Recover("main"); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err = store.Claim("main")
	if err != nil || !ok || delivery.Message.ID != receipt.ID {
		t.Fatalf("recovered claim = %+v, %v, %v", delivery, ok, err)
	}
	if err := store.Ack(delivery); err != nil {
		t.Fatal(err)
	}
	if err := store.Recover("main"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Claim("main"); err != nil || ok {
		t.Fatalf("acked message returned: ok=%v err=%v", ok, err)
	}
}

func TestListenerReportsWakeWithoutClaiming(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-inbox-consumer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	store := Store{Root: root}
	listener, err := StartListener(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	receipt, err := store.Notify(Request{To: "main", Source: "test", Event: "two", Body: "live"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case target := <-listener.Hints():
		if target != "main" {
			t.Fatalf("wake target = %q", target)
		}
	case <-time.After(time.Second):
		t.Fatal("live wake was not reported")
	}
	notice, err := store.Read("main", receipt.ID)
	if err != nil || notice.Status != StatusNew {
		t.Fatalf("notice was claimed: status=%s err=%v", notice.Status, err)
	}
}

func TestListenerExposesWorkflowHintWithoutClaimingIt(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-inbox-hint-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	store := Store{Root: root}
	listener, err := StartListener(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	receipt, err := store.Notify(Request{To: "wrk:demo/run-1", Source: "test", Event: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case target := <-listener.Hints():
		if target != "wrk:demo/run-1" {
			t.Fatalf("hint = %q", target)
		}
	case <-time.After(time.Second):
		t.Fatal("other target hint was not exposed")
	}
	delivery, ok, err := store.Claim("wrk:demo/run-1")
	if err != nil || !ok || delivery.Message.ID != receipt.ID {
		t.Fatalf("durable message = %+v, ok=%v, err=%v", delivery.Message, ok, err)
	}
}

func TestOnlyOneListenerOwnsStateRoot(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "shell3-inbox-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	first, err := StartListener(t.Context(), Store{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := StartListener(t.Context(), Store{Root: root}); err == nil || !strings.Contains(err.Error(), "another wake listener") {
		t.Fatalf("second listener error = %v", err)
	}
}

func TestArchiveRetainsNoticeAndListPagesMetadata(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	store := Store{Root: root, Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	var ids []string
	for _, body := range []string{"first full body", "second full body", "third full body"} {
		receipt, err := store.Notify(Request{To: "main", Source: "test", Event: "done", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, receipt.ID)
	}
	delivery, ok, err := store.Claim("main")
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := store.Archive(delivery); err != nil {
		t.Fatal(err)
	}
	archived, err := store.Read("main", delivery.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != StatusArchived || archived.Message.Body != delivery.Message.Body {
		t.Fatalf("archived notice = %+v", archived)
	}
	pending, total, err := store.List("main", StatusPending, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(pending) != 1 || pending[0].Message.ID == delivery.Message.ID {
		t.Fatalf("pending page = %+v total=%d", pending, total)
	}
	all, total, err := store.List("main", StatusAll, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(all) != 3 || all[0].Message.ID != ids[0] {
		t.Fatalf("all notices = %+v total=%d", all, total)
	}
	foundArchived := false
	for _, notice := range all {
		foundArchived = foundArchived || notice.Message.ID == delivery.Message.ID && notice.Status == StatusArchived
	}
	if !foundArchived {
		t.Fatalf("archived notice missing from all page: %+v", all)
	}
}

func TestReadRejectsTraversalMessageID(t *testing.T) {
	_, err := (Store{Root: t.TempDir()}).Read("main", "../../secret")
	if err == nil || !strings.Contains(err.Error(), "invalid message id") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadProgressRequiresSequentialCompleteRead(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if _, err := store.Notify(Request{To: "main", Source: "test", Event: "read", Body: "abcdefghij"}); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := store.Claim("main")
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := store.RecordRead("main", delivery.Message.ID, 5, 10, 10); err == nil {
		t.Fatal("skipped prefix was accepted")
	}
	if err := store.RecordRead("main", delivery.Message.ID, 0, 5, 10); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRead("main", delivery.Message.ID, 0, 5, 10); err != nil {
		t.Fatalf("idempotent reread: %v", err)
	}
	if full, err := store.FullyRead(delivery); err != nil || full {
		t.Fatalf("partial full=%v err=%v", full, err)
	}
	if err := store.RecordRead("main", delivery.Message.ID, 5, 10, 10); err != nil {
		t.Fatal(err)
	}
	if full, err := store.FullyRead(delivery); err != nil || !full {
		t.Fatalf("complete full=%v err=%v", full, err)
	}
}

func TestArchiveReadRejectsBatchUntilEveryNoticeIsFullyRead(t *testing.T) {
	store := Store{Root: t.TempDir()}
	var deliveries []Delivery
	for _, body := range []string{"first", "second"} {
		if _, err := store.Notify(Request{To: "main", Source: "test", Event: "read", Body: body}); err != nil {
			t.Fatal(err)
		}
		delivery, ok, err := store.Claim("main")
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := store.RecordRead("main", deliveries[0].Message.ID, 0, 5, 5); err != nil {
		t.Fatal(err)
	}
	ids := []string{deliveries[0].Message.ID, deliveries[1].Message.ID}
	if err := store.ArchiveRead("main", ids); err == nil {
		t.Fatal("batch with unread notice was archived")
	}
	first, err := store.Read("main", ids[0])
	if err != nil || first.Status != StatusProcessing {
		t.Fatalf("first status=%s err=%v", first.Status, err)
	}
	if err := store.RecordRead("main", ids[1], 0, 6, 6); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveRead("main", ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		notice, err := store.Read("main", id)
		if err != nil || notice.Status != StatusArchived {
			t.Fatalf("notice %s status=%s err=%v", id, notice.Status, err)
		}
	}
}
