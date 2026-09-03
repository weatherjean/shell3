//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/inbox"
)

func TestInboxListAndReadAreBounded(t *testing.T) {
	root := t.TempDir()
	store := inbox.Store{Root: root}
	var wanted string
	for i := range 12 {
		body := strings.Repeat(string(rune('a'+i)), defaultNoticeReadLimit+100)
		receipt, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			wanted = receipt.ID
		}
	}
	command := newInboxCommand()
	var listOut bytes.Buffer
	command.SetOut(&listOut)
	command.SetArgs([]string{"--state", root, "list"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var list noticeListOutput
	if err := json.Unmarshal(listOut.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 12 || len(list.Notices) != 10 || list.NextOffset == nil || *list.NextOffset != 10 {
		t.Fatalf("list = %+v", list)
	}
	if len([]rune(list.Notices[0].Preview)) > noticePreviewRunes {
		t.Fatalf("preview was not bounded: %d", len([]rune(list.Notices[0].Preview)))
	}

	command = newInboxCommand()
	var readOut bytes.Buffer
	command.SetOut(&readOut)
	command.SetArgs([]string{"--state", root, "read", wanted})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var read noticeReadOutput
	if err := json.Unmarshal(readOut.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Body) != defaultNoticeReadLimit || read.NextOffset == nil || *read.NextOffset != defaultNoticeReadLimit {
		t.Fatalf("read = body bytes %d next %v", len(read.Body), read.NextOffset)
	}
}

func TestBoundedNoticeBodyPreservesUTF8Boundaries(t *testing.T) {
	body, next, err := boundedNoticeBody("ééé", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if body != "é" || next == nil || *next != 2 {
		t.Fatalf("body=%q next=%v", body, next)
	}
	if _, _, err := boundedNoticeBody("éé", 1, 2); err == nil {
		t.Fatal("mid-rune offset was accepted")
	}
}

func TestInboxReadThenBatchArchive(t *testing.T) {
	root := t.TempDir()
	store := inbox.Store{Root: root}
	var ids []string
	for _, body := range []string{"one", "two"} {
		receipt, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, receipt.ID)
	}

	command := newInboxCommand()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"--state", root, "archive", strings.Join(ids, ",")})
	if err := command.Execute(); err == nil {
		t.Fatal("unread notices were archived")
	}

	for _, id := range ids {
		command = newInboxCommand()
		command.SetOut(&bytes.Buffer{})
		command.SetArgs([]string{"--state", root, "read", id})
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	command = newInboxCommand()
	command.SetOut(&out)
	command.SetArgs([]string{"--state", root, "archive", strings.Join(ids, ",")})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		notice, err := store.Read("main", id)
		if err != nil || notice.Status != inbox.StatusArchived {
			t.Fatalf("notice %s status=%s err=%v", id, notice.Status, err)
		}
	}
}

func TestInboxDefaultRootAndConcurrentArchiveRemainSafe(t *testing.T) {
	home := t.TempDir()
	original := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = original })
	root := filepath.Join(home, ".shell3", "workdir", ".shell3_project")
	store := inbox.Store{Root: root}
	receipt, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	read := newInboxCommand()
	read.SetOut(&bytes.Buffer{})
	read.SetArgs([]string{"read", receipt.ID})
	if err := read.Execute(); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			cmd := newInboxCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs([]string{"archive", receipt.ID})
			errs <- cmd.Execute()
		}()
	}
	var successes int
	for range 2 {
		if err := <-errs; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("archive successes = %d, want 1", successes)
	}
	notice, err := store.Read("main", receipt.ID)
	if err != nil || notice.Status != inbox.StatusArchived {
		t.Fatalf("notice status=%s err=%v", notice.Status, err)
	}
}
