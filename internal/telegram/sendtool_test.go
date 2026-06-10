//go:build unix

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendMediaTool_RegisteredAndSends(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")

	if !b.hasTool("send_media_telegram") {
		t.Fatal("send_media_telegram should be registered in the schema")
	}

	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":"report.txt","caption":"here"}`)
	if !strings.Contains(out, "sent report.txt") {
		t.Fatalf("unexpected result: %q", out)
	}
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "report.txt" || string(doc.data) != "hello" || doc.caption != "here" {
		t.Fatalf("document not sent correctly: %+v ok=%v", doc, ok)
	}
}

func TestSendMediaTool_RefusesEnv(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	dir := t.TempDir()
	b.SetWorkDir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := b.sendMediaHandler(context.Background(), `{"path":".env"}`)
	if !strings.Contains(out, "refusing") {
		t.Fatalf("expected refusal for .env, got %q", out)
	}
	if _, ok := fc.lastDoc(); ok {
		t.Fatal(".env must not be sent")
	}
}
